package tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/dchest/uniuri"

	driver "github.com/arangodb/go-driver"
	api "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1alpha"
	"github.com/arangodb/kube-arangodb/pkg/client"
)


// TestImmutableStorageEngine
// Tests that storage engine of deployed cluster cannot be changed
func TestImmutableStorageEngine(t *testing.T) {
	longOrSkip(t)
	c := client.MustNewInCluster()
	kubecli := mustNewKubeClient(t)
	ns := getNamespace(t)

	// Prepare deployment config
	depl := newDeployment("test-ise-" + uniuri.NewLen(4))
	depl.Spec.Mode = api.DeploymentModeCluster
	depl.Spec.SetDefaults(depl.GetName())

	// Create deployment
	apiObject, err := c.DatabaseV1alpha().ArangoDeployments(ns).Create(depl)
	if err != nil {
		t.Fatalf("Create deployment failed: %v", err)
	}

	// Wait for deployment to be ready
	if _, err := waitUntilDeployment(c, depl.GetName(), ns, deploymentHasState(api.DeploymentStateRunning)); err != nil {
		t.Fatalf("Deployment not running in time: %v", err)
	}

	// Create a database client
	ctx := context.Background()
	client := mustNewArangodDatabaseClient(ctx, kubecli, apiObject, t)

	// Wait for cluster to be completely ready
	if err := waitUntilClusterHealth(client, func(h driver.ClusterHealth) error {
		return clusterHealthEqualsSpec(h, apiObject.Spec)
	}); err != nil {
		t.Fatalf("Cluster not running in expected health in time: %v", err)
	}

	// Try to reset storageEngine
	if _, err := updateDeployment(c, depl.GetName(), ns,
		func(spec *api.DeploymentSpec) {
			spec.StorageEngine = api.StorageEngineMMFiles
		}); err != nil {
			t.Fatalf("Failed to update the StorageEngine setting: %v", err)
		} 

	// Wait for StorageEngine parameter to be back to RocksDB
	if _, err := waitUntilDeployment(c, depl.GetName(), ns,
		func(depl *api.ArangoDeployment) error {
			if depl.Spec.StorageEngine == api.StorageEngineRocksDB {
				return nil
			} 
			return fmt.Errorf("StorageEngine not back to %s", api.StorageEngineRocksDB)
		}); err != nil {
			t.Fatalf("StorageEngine parameter not immutable: %v", err)
		}

	
	// Cleanup
	removeDeployment(c, depl.GetName(), ns)
}
