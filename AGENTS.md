# AGENTS.md

Guidance for AI coding agents working in the **Kruise Rollouts** repository.

## Project Overview

Kruise Rollouts is an advanced **progressive delivery controller** for Kubernetes. It is a non-intrusive, plug-and-play companion to native and OpenKruise workloads, providing canary, multi-batch, A/B testing, and blue-green release strategies along with fine-grained traffic orchestration.

- Module path: `github.com/openkruise/rollouts`
- Language: **Go 1.20** (project requires ≥ 1.18; Dockerfiles use 1.20)
- Kubernetes: **≥ 1.19** (envtest uses 1.28.0)
- Main entrypoint: [main.go](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/main.go)
- Manager binary output: `bin/manager`

## Tech Stack

- `sigs.k8s.io/controller-runtime` v0.16.6 — operator framework
- `k8s.io/api`, `client-go`, `apimachinery` v0.28.9
- `sigs.k8s.io/gateway-api` v0.8.1 — Gateway API traffic routing
- `github.com/yuin/gopher-lua` — pluggable Lua scripts for custom gateways (Istio, Apisix, Higress, MSE, Nginx, ALB, …)
- `github.com/openkruise/kruise-api` v1.7.0 — CloneSet, Advanced StatefulSet, Advanced DaemonSet integration
- `ginkgo` v1.16.5 + `gomega` v1.27.10 for E2E; `testify` for unit tests

## Repository Layout

```
api/                  CRD type definitions (v1alpha1, v1beta1) + zz_generated.deepcopy.go
config/               Kustomize manifests: CRDs, RBAC, webhook, manager Deployment, Prometheus
docs/                 Proposals, tutorials, contributing/debug guides
hack/                 boilerplate.go.txt header for generated code
lua_configuration/    Lua scripts for Istio VS/DR and ingresses (alb, higress, mse, nginx)
pkg/
├── client/           Generated typed clientsets (do not edit manually)
├── controller/
│   ├── batchrelease/   BatchRelease reconciler + canary/bluegreen/partition controllers
│   ├── deployment/     Advanced Deployment controller
│   ├── nativedaemonset/Native DaemonSet progressive delivery
│   ├── rollout/        Top-level Rollout reconciler & state machine
│   ├── rollouthistory/ RolloutHistory lifecycle
│   └── trafficrouting/ Traffic routing reconciler
├── feature/          Feature gate registry
├── trafficrouting/   Network providers (gateway, ingress, custom Lua) + manager
├── util/             Shared helpers: client, labels, patch, luamanager, grace, expectation
└── webhook/          Mutating/validating webhooks for Rollout and workloads
test/e2e/             Ginkgo E2E suites (rollout, deployment, v1beta1)
scripts/              deploy_kind.sh and other dev scripts
```

## Build, Test, Run

All workflows are driven by the [Makefile](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/Makefile). Common targets:

| Target              | Purpose                                                              |
| ------------------- | -------------------------------------------------------------------- |
| `make manifests`    | Regenerate CRDs, RBAC, webhook YAML via `controller-gen v0.14.0`     |
| `make generate`     | Regenerate `zz_generated.deepcopy.go` (uses `hack/boilerplate.go.txt`) |
| `make fmt`          | `go fmt ./...`                                                       |
| `make vet`          | `go vet ./...`                                                       |
| `make test`         | Runs `manifests generate fmt vet envtest` then `go test -race ./pkg/... -coverprofile raw-cover.out`; coverage written to `cover.out` (excluding `pkg/client`) |
| `make build`        | Build manager binary to `bin/manager`                                |
| `make run`          | Run controller against current kubeconfig                            |
| `make docker-build` / `docker-push` / `docker-multiarch` | Container image (multi-arch via `buildx`, default platforms `linux/amd64,linux/arm64`) |
| `make install` / `uninstall` | Apply/remove CRDs into the cluster from `~/.kube/config`     |
| `make deploy` / `undeploy`   | Deploy/remove the controller stack via `config/default`      |

Tools auto-installed under `bin/` and `testbin/` on first invocation:
`controller-gen v0.14.0`, `kustomize v4@v4.5.5`, `ginkgo@v1.16.4`, `helm v3@v3.14.0`, `setup-envtest` (K8s `1.28.0`).

## Mandatory Workflow After Code Changes

1. If you modified anything under `api/` (types, kubebuilder markers):
   - Run `make generate` (deepcopy)
   - Run `make manifests` (CRD/RBAC/webhook YAML in `config/`)
2. Run `make fmt vet`.
3. Run `make test` (or at least `go test ./pkg/<changed-package>/...`).
4. Do **not** hand-edit generated files: `api/**/zz_generated.deepcopy.go`, `pkg/client/**`, `config/crd/bases/*`, RBAC role.yaml, `config/webhook/manifests.yaml`.
5. After adding/removing dependencies, run `go mod tidy`.

## Coding Conventions

- Standard Go style: `gofmt` + `go vet` clean. The repo uses [.golangci.yml](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/.golangci.yml) — keep it green.
- File header: prepend [hack/boilerplate.go.txt](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/hack/boilerplate.go.txt) (Apache 2.0) to every new `.go` file.
- API versions: `v1alpha1` is legacy; new fields land in `v1beta1` with conversion in [api/v1alpha1/conversion.go](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/api/v1alpha1/conversion.go) and [api/v1beta1/convertion.go](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/api/v1beta1/convertion.go). Keep round-trip tests passing.
- Controllers follow the controller-runtime reconciler pattern with predicates/event filters; prefer `pkg/util/expectation` and `pkg/util/patch` over ad-hoc retries.
- Logging: `k8s.io/klog/v2`. Use structured key-value logging.
- Error handling: return wrapped errors; never panic in reconcilers.
- Feature gates: register in [pkg/feature/rollout_features.go](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/pkg/feature/rollout_features.go).

## Traffic Routing & Lua

Custom gateway/ingress integrations are driven by Lua scripts under [lua_configuration/](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/lua_configuration). When adding a new provider:

1. Drop the Lua script under the matching `networking.<group>/<Kind>/` or `trafficrouting_ingress/` directory.
2. Add a `testdata` folder beside the script with input/output fixtures.
3. Register/load via [pkg/util/luamanager](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/pkg/util/luamanager) and [pkg/trafficrouting/network](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/pkg/trafficrouting/network).
4. Verify with [lua_configuration/convert_test_case_to_lua_object.go](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/lua_configuration/convert_test_case_to_lua_object.go) helpers.

## Testing Notes

- Unit tests live next to source as `*_test.go`. Use `testify` for assertions where present.
- `make test` requires envtest assets — let the Makefile fetch them; do not commit `testbin/`.
- E2E tests live in [test/e2e](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/test/e2e) and are run with `ginkgo`; spin up a kind cluster via [scripts/deploy_kind.sh](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/scripts/deploy_kind.sh).
- Race detector is mandatory in CI (`go test -race`).

## Proposals & Docs

- New features that introduce APIs or significant behavior require a proposal in [docs/proposals/](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/docs/proposals).
- Tutorials live under [docs/tutorials/](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/docs/tutorials); contributor/debug docs under [docs/contributing/](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/docs/contributing).

## Git & PR Workflow

- Default branch: `master`. Fork → feature branch → PR. Never push to `master` directly.
- Follow the [PR template](file:///Users/shouchen/git/kruise-rollout/src/github.com/openkruise/rollouts/.github/PULL_REQUEST_TEMPLATE.md).
- Keep commits focused; rebase onto `upstream/master` before pushing.
- Do **not** modify `git config`, force-push to `master`, or skip hooks.

## Quick Sanity Checklist Before Handing Off

- [ ] `make manifests generate` clean (no unstaged generated diffs unless intended)
- [ ] `make fmt vet` passes
- [ ] `make test` passes locally
- [ ] Boilerplate header present on new Go files
- [ ] No edits under `pkg/client/`, `zz_generated.*`, or `config/crd/bases/` unless regenerated
- [ ] `go.mod` / `go.sum` tidy when dependencies changed
