//
// DISCLAIMER
//
// Copyright 2020 ArangoDB GmbH, Cologne, Germany
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Copyright holder is ArangoDB GmbH, Cologne, Germany
//
// Author Ewout Prangsma
//

package resources

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/arangodb/kube-arangodb/pkg/deployment/pod"
	"github.com/pkg/errors"

	"github.com/arangodb/kube-arangodb/pkg/util/constants"
	jg "github.com/dgrijalva/jwt-go"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	"github.com/arangodb/kube-arangodb/pkg/metrics"
	"github.com/arangodb/kube-arangodb/pkg/util/k8sutil"
)

var (
	inspectedSecretsCounters     = metrics.MustRegisterCounterVec(metricsComponent, "inspected_secrets", "Number of Secret inspections per deployment", metrics.DeploymentName)
	inspectSecretsDurationGauges = metrics.MustRegisterGaugeVec(metricsComponent, "inspect_secrets_duration", "Amount of time taken by a single inspection of all Secrets for a deployment (in sec)", metrics.DeploymentName)
)

// EnsureSecrets creates all secrets needed to run the given deployment
func (r *Resources) EnsureSecrets() error {
	start := time.Now()
	kubecli := r.context.GetKubeCli()
	ns := r.context.GetNamespace()
	secrets := k8sutil.NewSecretCache(kubecli.CoreV1().Secrets(ns))
	spec := r.context.GetSpec()
	deploymentName := r.context.GetAPIObject().GetName()
	defer metrics.SetDuration(inspectSecretsDurationGauges.WithLabelValues(deploymentName), start)
	counterMetric := inspectedSecretsCounters.WithLabelValues(deploymentName)

	if spec.IsAuthenticated() {
		counterMetric.Inc()
		if err := r.ensureTokenSecret(secrets, spec.Authentication.GetJWTSecretName()); err != nil {
			return maskAny(err)
		}

		if spec.Metrics.IsEnabled() {
			if err := r.ensureExporterTokenSecret(secrets, spec.Metrics.GetJWTTokenSecretName(), spec.Authentication.GetJWTSecretName()); err != nil {
				return maskAny(err)
			}
		}
	}
	if spec.IsSecure() {
		counterMetric.Inc()
		if err := r.ensureTLSCACertificateSecret(secrets, spec.TLS); err != nil {
			return maskAny(err)
		}
	}
	if spec.RocksDB.IsEncrypted() {
		if err := r.ensureEncryptionKeyfolderSecret(secrets, spec.RocksDB.Encryption.GetKeySecretName(), pod.GetKeyfolderSecretName(deploymentName)); err != nil {
			return maskAny(err)
		}
	}
	if spec.Sync.IsEnabled() {
		counterMetric.Inc()
		if err := r.ensureTokenSecret(secrets, spec.Sync.Authentication.GetJWTSecretName()); err != nil {
			return maskAny(err)
		}
		counterMetric.Inc()
		if err := r.ensureTokenSecret(secrets, spec.Sync.Monitoring.GetTokenSecretName()); err != nil {
			return maskAny(err)
		}
		counterMetric.Inc()
		if err := r.ensureTLSCACertificateSecret(secrets, spec.Sync.TLS); err != nil {
			return maskAny(err)
		}
		counterMetric.Inc()
		if err := r.ensureClientAuthCACertificateSecret(secrets, spec.Sync.Authentication); err != nil {
			return maskAny(err)
		}
	}
	return nil
}

// ensureTokenSecret checks if a secret with given name exists in the namespace
// of the deployment. If not, it will add such a secret with a random
// token.
func (r *Resources) ensureTokenSecret(secrets k8sutil.SecretInterface, secretName string) error {
	if _, err := secrets.Get(secretName, meta.GetOptions{}); k8sutil.IsNotFound(err) {
		// Secret not found, create it
		// Create token
		tokenData := make([]byte, 32)
		rand.Read(tokenData)
		token := hex.EncodeToString(tokenData)

		// Create secret
		owner := r.context.GetAPIObject().AsOwner()
		if err := k8sutil.CreateTokenSecret(secrets, secretName, token, &owner); k8sutil.IsAlreadyExists(err) {
			// Secret added while we tried it also
			return nil
		} else if err != nil {
			// Failed to create secret
			return maskAny(err)
		}
	} else if err != nil {
		// Failed to get secret for other reasons
		return maskAny(err)
	}
	return nil
}

func (r *Resources) ensureEncryptionKeyfolderSecret(secrets k8sutil.SecretInterface, keyfileSecretName, secretName string) error {
	keyfile, err := secrets.Get(keyfileSecretName, meta.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "Unable to find original secret")
	}

	if len(keyfile.Data) == 0 {
		return errors.Errorf("Missing key in secret")
	}

	d, ok := keyfile.Data[constants.SecretEncryptionKey]
	if !ok {
		return errors.Errorf("Missing key in secret")
	}

	owner := r.context.GetAPIObject().AsOwner()
	if err = k8sutil.AppendKeyfileToKeyfolder(secrets, &owner, secretName, d); err != nil {
		return errors.Wrapf(err, "Unable to create keyfolder secret")
	}
	return nil
}

var (
	exporterTokenClaims = map[string]interface{}{
		"iss":           "arangodb",
		"server_id":     "exporter",
		"allowed_paths": []string{"/_admin/statistics", "/_admin/statistics-description", k8sutil.ArangoExporterInternalEndpoint},
	}
)

// ensureExporterTokenSecret checks if a secret with given name exists in the namespace
// of the deployment. If not, it will add such a secret with correct access.
func (r *Resources) ensureExporterTokenSecret(secrets k8sutil.SecretInterface, tokenSecretName, secretSecretName string) error {
	if recreate, exists, err := r.ensureExporterTokenSecretCreateRequired(secrets, tokenSecretName, secretSecretName); err != nil {
		return err
	} else if recreate {
		// Create secret
		if exists {
			if err := secrets.Delete(tokenSecretName, nil); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}

		owner := r.context.GetAPIObject().AsOwner()
		if err := k8sutil.CreateJWTFromSecret(secrets, tokenSecretName, secretSecretName, exporterTokenClaims, &owner); k8sutil.IsAlreadyExists(err) {
			// Secret added while we tried it also
			return nil
		} else if err != nil {
			// Failed to create secret
			return maskAny(err)
		}
	}
	return nil
}

func (r *Resources) ensureExporterTokenSecretCreateRequired(secrets k8sutil.SecretInterface, tokenSecretName, secretSecretName string) (bool, bool, error) {
	if secret, err := secrets.Get(tokenSecretName, meta.GetOptions{}); k8sutil.IsNotFound(err) {
		return true, false, nil
	} else if err == nil {
		// Check if claims are fine
		data, ok := secret.Data[constants.SecretKeyToken]
		if !ok {
			return true, true, nil
		}

		secret, err := k8sutil.GetTokenSecret(secrets, secretSecretName)
		if err != nil {
			return false, true, maskAny(err)
		}

		token, err := jg.Parse(string(data), func(token *jg.Token) (i interface{}, err error) {
			return []byte(secret), nil
		})

		if err != nil {
			return true, true, nil
		}

		tokenClaims, ok := token.Claims.(jg.MapClaims)
		if !ok {
			return true, true, nil
		}

		return !equality.Semantic.DeepEqual(tokenClaims, exporterTokenClaims), true, nil
	} else {
		// Failed to get secret for other reasons
		return false, false, maskAny(err)
	}
}

// ensureTLSCACertificateSecret checks if a secret with given name exists in the namespace
// of the deployment. If not, it will add such a secret with a generated CA certificate.
func (r *Resources) ensureTLSCACertificateSecret(secrets k8sutil.SecretInterface, spec api.TLSSpec) error {
	if _, err := secrets.Get(spec.GetCASecretName(), meta.GetOptions{}); k8sutil.IsNotFound(err) {
		// Secret not found, create it
		apiObject := r.context.GetAPIObject()
		owner := apiObject.AsOwner()
		deploymentName := apiObject.GetName()
		if err := createTLSCACertificate(r.log, secrets, spec, deploymentName, &owner); k8sutil.IsAlreadyExists(err) {
			// Secret added while we tried it also
			return nil
		} else if err != nil {
			// Failed to create secret
			return maskAny(err)
		}
	} else if err != nil {
		// Failed to get secret for other reasons
		return maskAny(err)
	}
	return nil
}

// ensureClientAuthCACertificateSecret checks if a secret with given name exists in the namespace
// of the deployment. If not, it will add such a secret with a generated CA certificate.
func (r *Resources) ensureClientAuthCACertificateSecret(secrets k8sutil.SecretInterface, spec api.SyncAuthenticationSpec) error {
	if _, err := secrets.Get(spec.GetClientCASecretName(), meta.GetOptions{}); k8sutil.IsNotFound(err) {
		// Secret not found, create it
		apiObject := r.context.GetAPIObject()
		owner := apiObject.AsOwner()
		deploymentName := apiObject.GetName()
		if err := createClientAuthCACertificate(r.log, secrets, spec, deploymentName, &owner); k8sutil.IsAlreadyExists(err) {
			// Secret added while we tried it also
			return nil
		} else if err != nil {
			// Failed to create secret
			return maskAny(err)
		}
	} else if err != nil {
		// Failed to get secret for other reasons
		return maskAny(err)
	}
	return nil
}

// getJWTSecret loads the JWT secret from a Secret configured in apiObject.Spec.Authentication.JWTSecretName.
func (r *Resources) getJWTSecret(spec api.DeploymentSpec) (string, error) {
	if !spec.IsAuthenticated() {
		return "", nil
	}
	kubecli := r.context.GetKubeCli()
	ns := r.context.GetNamespace()
	secrets := kubecli.CoreV1().Secrets(ns)
	secretName := spec.Authentication.GetJWTSecretName()
	s, err := k8sutil.GetTokenSecret(secrets, secretName)
	if err != nil {
		r.log.Debug().Err(err).Str("secret-name", secretName).Msg("Failed to get JWT secret")
		return "", maskAny(err)
	}
	return s, nil
}

// getSyncJWTSecret loads the JWT secret used for syncmasters from a Secret configured in apiObject.Spec.Sync.Authentication.JWTSecretName.
func (r *Resources) getSyncJWTSecret(spec api.DeploymentSpec) (string, error) {
	kubecli := r.context.GetKubeCli()
	ns := r.context.GetNamespace()
	secrets := kubecli.CoreV1().Secrets(ns)
	secretName := spec.Sync.Authentication.GetJWTSecretName()
	s, err := k8sutil.GetTokenSecret(secrets, secretName)
	if err != nil {
		r.log.Debug().Err(err).Str("secret-name", secretName).Msg("Failed to get sync JWT secret")
		return "", maskAny(err)
	}
	return s, nil
}

// getSyncMonitoringToken loads the token secret used for monitoring sync masters & workers.
func (r *Resources) getSyncMonitoringToken(spec api.DeploymentSpec) (string, error) {
	kubecli := r.context.GetKubeCli()
	ns := r.context.GetNamespace()
	secrets := kubecli.CoreV1().Secrets(ns)
	secretName := spec.Sync.Monitoring.GetTokenSecretName()
	s, err := k8sutil.GetTokenSecret(secrets, secretName)
	if err != nil {
		r.log.Debug().Err(err).Str("secret-name", secretName).Msg("Failed to get sync monitoring secret")
		return "", maskAny(err)
	}
	return s, nil
}
