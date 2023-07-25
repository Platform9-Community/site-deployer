REGISTRY ?= jmv2
DEPLOY_IMAGE_NAME ?= deployrunner
DEPLOY_IMAGE = $(REGISTRY)/$(DEPLOY_IMAGE_NAME)
DEPLOY_VERSION = $(shell jq --raw-output '.version' <VERSION.json)

clean:
	docker image rm -f ${DEPLOY_IMAGE}:${DEPLOY_VERSION} || true

#image:
#	@docker image inspect ${DEPLOY_IMAGE}:${DEPLOY_VERSION} >/dev/null 2>&1 || \
#		docker build -t ${DEPLOY_IMAGE}:${DEPLOY_VERSION} .

image:
	@docker build -t ${DEPLOY_IMAGE}:${DEPLOY_VERSION} .

push_image:
	@echo Pushing image -> ${REGISTRY} ${DEPLOY_IMAGE}
	docker image tag ${DEPLOY_IMAGE}:${DEPLOY_VERSION} ${DEPLOY_IMAGE}:latest
	docker image push ${DEPLOY_IMAGE}:${DEPLOY_VERSION}
	docker image push ${DEPLOY_IMAGE}:latest

verify: image
	docker run \
		--rm \
		--name ${DEPLOY_IMAGE_NAME} \
		--volume $$(pwd):/foundation \
		${DEPLOY_IMAGE}:${DEPLOY_VERSION} test/verify.sh

shellcheck:
	bash -c 'shopt -s globstar; shellcheck **/*.sh'

bump:
	docker run \
		--rm \
		--name ${DEPLOY_IMAGE_NAME} \
		--volume /var/run/docker.sock:/var/run/docker.sock \
		--volume $$(pwd):/foundation \
		${DEPLOY_IMAGE}:latest \
		whoami
