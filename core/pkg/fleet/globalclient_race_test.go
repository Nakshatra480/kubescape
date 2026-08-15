//go:build fleetrace

// This file is excluded from normal builds on purpose.
//
// It demonstrates the reason the fleet orchestrator scans sequentially: the
// Kubernetes client configuration in k8s-interface is process-global and
// unguarded, so two goroutines pointing the process at different clusters race
// on it. The test is expected to FAIL under the race detector. That failure is
// the result.
//
//	go test -race -tags fleetrace ./core/pkg/fleet/ -run TestGlobalClientConfigRaces
//
// Once the client becomes per-scan, this test stops reporting a race and
// concurrent fleet scanning becomes safe to build.
package fleet

import (
	"sync"
	"testing"

	"github.com/kubescape/k8s-interface/k8sinterface"
)

// SetClusterContextName writes the package-level clusterContextName, and
// LoadK8sConfig reads it on its first line. Neither takes a lock, so a fleet
// scan that re-pointed the process from another goroutine would be reading a
// context name that is being rewritten underneath it.
func TestGlobalClientConfigRaces(t *testing.T) {
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			k8sinterface.SetClusterContextName("cluster-a")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			k8sinterface.SetClusterContextName("cluster-b")
			_ = k8sinterface.LoadK8sConfig()
		}
	}()

	wg.Wait()

	t.Log("if the race detector reported nothing here, the global client config has been made per-scan and concurrent fleet scanning can be revisited")
}
