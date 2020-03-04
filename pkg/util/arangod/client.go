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

package arangod

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	nhttp "net/http"
	"strconv"
	"time"

	driver "github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/agency"
	"github.com/arangodb/go-driver/http"
	"github.com/arangodb/go-driver/jwt"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	api "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	"github.com/arangodb/kube-arangodb/pkg/util/k8sutil"
)

type (
	// skipAuthenticationKey is the context key used to indicate NOT setting any authentication
	skipAuthenticationKey struct{}
	// requireAuthenticationKey is the context key used to indicate that authentication is required
	requireAuthenticationKey struct{}
)

// WithSkipAuthentication prepares a context that when given to functions in
// this file will avoid creating any authentication for arango clients.
func WithSkipAuthentication(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipAuthenticationKey{}, true)
}

// WithRequireAuthentication prepares a context that when given to functions in
// this file will fail when authentication is not available.
func WithRequireAuthentication(ctx context.Context) context.Context {
	return context.WithValue(ctx, requireAuthenticationKey{}, true)
}

var (
	sharedHTTPTransport = &nhttp.Transport{
		Proxy: nhttp.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 90 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	sharedHTTPSTransport = &nhttp.Transport{
		Proxy: nhttp.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 90 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	sharedHTTPTransportShortTimeout = &nhttp.Transport{
		Proxy: nhttp.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 100 * time.Millisecond,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       100 * time.Millisecond,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	sharedHTTPSTransportShortTimeout = &nhttp.Transport{
		Proxy: nhttp.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 100 * time.Millisecond,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       100 * time.Millisecond,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
)

// CreateArangodClient creates a go-driver client for a specific member in the given group.
func CreateArangodClient(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment, group api.ServerGroup, id string) (driver.Client, error) {
	// Create connection
	dnsName := k8sutil.CreatePodDNSName(apiObject, group.AsRole(), id)
	c, err := createArangodClientForDNSName(ctx, cli, apiObject, dnsName, false)
	if err != nil {
		return nil, maskAny(err)
	}
	return c, nil
}

// CreateArangodDatabaseClient creates a go-driver client for accessing the entire cluster (or single server).
func CreateArangodDatabaseClient(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment, shortTimeout bool) (driver.Client, error) {
	// Create connection
	dnsName := k8sutil.CreateDatabaseClientServiceDNSName(apiObject)
	c, err := createArangodClientForDNSName(ctx, cli, apiObject, dnsName, shortTimeout)
	if err != nil {
		return nil, maskAny(err)
	}
	return c, nil
}

// CreateArangodAgencyClient creates a go-driver client for accessing the agents of the given deployment.
func CreateArangodAgencyClient(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment) (agency.Agency, error) {
	var dnsNames []string
	for _, m := range apiObject.Status.Members.Agents {
		dnsName := k8sutil.CreatePodDNSName(apiObject, api.ServerGroupAgents.AsRole(), m.ID)
		dnsNames = append(dnsNames, dnsName)
	}
	shortTimeout := false
	connConfig, err := createArangodHTTPConfigForDNSNames(ctx, cli, apiObject, dnsNames, shortTimeout)
	if err != nil {
		return nil, maskAny(err)
	}
	agencyConn, err := agency.NewAgencyConnection(connConfig)
	if err != nil {
		return nil, maskAny(err)
	}
	auth, err := createArangodClientAuthentication(ctx, cli, apiObject)
	if err != nil {
		return nil, maskAny(err)
	}
	if auth != nil {
		agencyConn, err = agencyConn.SetAuthentication(auth)
		if err != nil {
			return nil, maskAny(err)
		}
	}
	a, err := agency.NewAgency(agencyConn)
	if err != nil {
		return nil, maskAny(err)
	}
	return a, nil
}

// CreateArangodImageIDClient creates a go-driver client for an ArangoDB instance
// running in an Image-ID pod.
func CreateArangodImageIDClient(ctx context.Context, deployment k8sutil.APIObject, role, id string) (driver.Client, error) {
	// Create connection
	dnsName := k8sutil.CreatePodDNSName(deployment, role, id)
	c, err := createArangodClientForDNSName(ctx, nil, nil, dnsName, false)
	if err != nil {
		return nil, maskAny(err)
	}
	return c, nil
}

// CreateArangodClientForDNSName creates a go-driver client for a given DNS name.
func createArangodClientForDNSName(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment, dnsName string, shortTimeout bool) (driver.Client, error) {
	connConfig, err := createArangodHTTPConfigForDNSNames(ctx, cli, apiObject, []string{dnsName}, shortTimeout)
	if err != nil {
		return nil, maskAny(err)
	}
	// TODO deal with TLS with proper CA checking
	conn, err := http.NewConnection(connConfig)
	if err != nil {
		return nil, maskAny(err)
	}

	// Create client
	config := driver.ClientConfig{
		Connection: conn,
	}
	auth, err := createArangodClientAuthentication(ctx, cli, apiObject)
	if err != nil {
		return nil, maskAny(err)
	}
	config.Authentication = auth
	c, err := driver.NewClient(config)
	if err != nil {
		return nil, maskAny(err)
	}
	return c, nil
}

// createArangodHTTPConfigForDNSNames creates a go-driver HTTP connection config for a given DNS names.
func createArangodHTTPConfigForDNSNames(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment, dnsNames []string, shortTimeout bool) (http.ConnectionConfig, error) {
	scheme := "http"
	transport := sharedHTTPTransport
	if shortTimeout {
		transport = sharedHTTPTransportShortTimeout
	}
	if apiObject != nil && apiObject.Spec.IsSecure() {
		scheme = "https"
		transport = sharedHTTPSTransport
		if shortTimeout {
			transport = sharedHTTPSTransportShortTimeout
		}
	}
	connConfig := http.ConnectionConfig{
		Transport:          transport,
		DontFollowRedirect: true,
	}
	for _, dnsName := range dnsNames {
		connConfig.Endpoints = append(connConfig.Endpoints, scheme+"://"+net.JoinHostPort(dnsName, strconv.Itoa(k8sutil.ArangoPort)))
	}
	return connConfig, nil
}

// createArangodClientAuthentication creates a go-driver authentication for the servers in the given deployment.
func createArangodClientAuthentication(ctx context.Context, cli corev1.CoreV1Interface, apiObject *api.ArangoDeployment) (driver.Authentication, error) {
	if apiObject != nil && apiObject.Spec.IsAuthenticated() {
		// Authentication is enabled.
		// Should we skip using it?
		if ctx.Value(skipAuthenticationKey{}) == nil {
			secrets := cli.Secrets(apiObject.GetNamespace())
			s, err := k8sutil.GetTokenSecret(secrets, apiObject.Spec.Authentication.GetJWTSecretName())
			if err != nil {
				return nil, maskAny(err)
			}
			jwt, err := jwt.CreateArangodJwtAuthorizationHeader(s, "kube-arangodb")
			if err != nil {
				return nil, maskAny(err)
			}
			return driver.RawAuthentication(jwt), nil
		}
	} else {
		// Authentication is not enabled.
		if ctx.Value(requireAuthenticationKey{}) != nil {
			// Context requires authentication
			return nil, maskAny(fmt.Errorf("Authentication is required by context, but not provided in API object"))
		}
	}
	return nil, nil
}
