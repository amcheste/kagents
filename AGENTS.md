# AGENTS.md. Agent Team Guidelines for kagents

## When working as a teammate on this project

1. **Check the task list first**. Before starting work, check what's assigned to you
2. **Respect module boundaries**. Each internal package has a clear scope:
   - `internal/controller/`. Only reconciliation logic
   - `internal/claude/`. Only Claude Code file I/O and session management
   - `internal/budget/`. Only cost estimation
   - `internal/webhook/`. Only external notifications
   - `internal/metrics/`. Only Prometheus metrics
3. **Use kubebuilder markers**. All CRD types in `api/v1alpha1/` must have proper `+kubebuilder:` annotations
4. **Test with envtest**. Controller tests should use controller-runtime's envtest framework
5. **Follow Kubernetes conventions**. Conditions use `metav1.Condition`, status updates are separate from spec changes

## Architecture rules

- The operator NEVER makes Anthropic API calls directly. It only manages pods that run Claude Code
- All inter-agent communication goes through the shared PVC filesystem. The operator just creates and monitors the volumes
- Budget tracking is estimation-based. We can't read real-time token counts from Claude Code
- Pods use `RestartPolicy: Never`. Crashed agents get re-spawned fresh, not restarted

## Build verification

After any change, run:
```bash
make manifests generate fmt vet test
```

## Code style

- Go standard formatting (`gofmt`)
- Errors are wrapped with `fmt.Errorf("context: %w", err)`
- Logs use structured logging via `log.FromContext(ctx)`
- Constants go in the package that uses them, not a shared constants file
