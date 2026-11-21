#!/usr/bin/env bash

if [ -z "$IMG" ]; then
  echo "no found IMG env"
  exit 1
fi

set -e

# KUSTOMIZE_DIR selects the overlay to build; e2e workflows can point it at an
# overlay that enables extra feature gates (e.g. config/e2e-minready) instead
# of patching a positional args index in the deployment template.
KUSTOMIZE_DIR="${KUSTOMIZE_DIR:-config/default}"

make kustomize
KUSTOMIZE=$(pwd)/bin/kustomize
(cd config/manager && "${KUSTOMIZE}" edit set image controller="${IMG}")
"${KUSTOMIZE}" build "${KUSTOMIZE_DIR}" | sed -e 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/g' > /tmp/rollout-kustomization.yaml
echo -e "resources:\n- manager.yaml" > config/manager/kustomization.yaml
kubectl apply -f /tmp/rollout-kustomization.yaml
