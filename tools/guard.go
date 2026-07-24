package tools

import "context"

// AppMutationGuard vets whether a mutating operation may proceed on an app.
//
// Core tools are deliberately unaware of app "adoption" (a kubectl-managed
// workload brought into Epinio's view) — that concept lives entirely in the
// elevated package. To keep the dependency direction clean (elevated imports
// core, never the reverse), core exposes this seam and the elevated adoption
// tier injects a real guard via SetAppMutationGuard at registration. The
// default guard permits everything, so a pure-core build imposes no gate.
type AppMutationGuard interface {
	EnsureMutable(ctx context.Context, namespace, name, operation string) error
}

var appMutationGuard AppMutationGuard = permissiveGuard{}

// SetAppMutationGuard installs the active guard. Called by the elevated
// adoption tier when it registers; safe to call more than once. A nil guard
// is ignored so callers can't accidentally disable protection.
func SetAppMutationGuard(g AppMutationGuard) {
	if g != nil {
		appMutationGuard = g
	}
}

type permissiveGuard struct{}

func (permissiveGuard) EnsureMutable(context.Context, string, string, string) error {
	return nil
}
