GITHUB_RUN_ID ?= latest
KIND_CLUSTER_NAME ?= ci-testing
IMAGE ?= openkruise/kruise-rollout:e2e-$(GITHUB_RUN_ID)

.PHONY: build-image
build-image:
	docker build --pull --no-cache ../ -t $(IMAGE)

.PHONY: load-image
load-image: build-image
	kind load docker-image --name=$(KIND_CLUSTER_NAME) $(IMAGE)

.PHONY: install-kruise-rollout
install-kruise-rollout:
	@pushd .. > /dev/null && IMG=$(IMAGE) ./scripts/deploy_kind.sh && popd > /dev/null
	kubectl wait --namespace kruise-rollout \
    		--for=condition=ready pod \
    		--selector=control-plane=controller-manager \
    		--timeout=600s