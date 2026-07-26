package tools

import "context"

// AppMutationGuard vets whether a mutating operation may proceed on an app.
//
// Core tools are deliberately unaware of app "adoption" (a kubectl-managed
// workload brought into Epinio's view) — that concept lives entirely in the
// elevated package. To keep the dependency direction clean (elevated imports
// core, never the reverse), core exposes this seam and main installs the
// elevated adoption guard via SetAppMutationGuard once at startup. The default
// guard permits everything, so a pure-core build imposes no gate.
type AppMutationGuard interface {
	EnsureMutable(ctx context.Context, namespace, name, operation string) error
}

var appMutationGuard AppMutationGuard = permissiveGuard{}

// SetAppMutationGuard installs the active guard. Call it once at startup,
// before the server begins handling requests — the guard is a process-global
// read concurrently by tool handlers, so setting it after serving has begun
// would be a data race. A nil guard is ignored.
func SetAppMutationGuard(g AppMutationGuard) {
	if g != nil {
		appMutationGuard = g
	}
}

type permissiveGuard struct{}

func (permissiveGuard) EnsureMutable(
	context.Context,
	string, string, string,
) error {
	return nil
}
