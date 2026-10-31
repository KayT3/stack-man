package api

import "testing"

func TestMutating(t *testing.T) {
	for _, method := range []string{
		MethodContainersUp,
		MethodContainersDown,
		MethodContainersRestart,
		MethodContainersBuild,
		MethodContainersNetworkConnect,
		MethodContainersNetworkDisconnect,
		MethodGitPull,
		MethodCommandRun,
	} {
		if !Mutating(method) {
			t.Errorf("Mutating(%q) = false, want true", method)
		}
	}
	for _, method := range []string{
		MethodPing,
		MethodInfo,
		MethodStackList,
		MethodContainersPs,
		MethodContainersLogs,
		MethodCommandList,
		MethodJobList,
	} {
		if Mutating(method) {
			t.Errorf("Mutating(%q) = true, want false", method)
		}
	}
}
