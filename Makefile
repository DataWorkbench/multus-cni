.PHONY: push
push:
	docker build -t dockerhub.databench.io/databench/nic-manager:latest -f images/Dockerfile .
	docker push dockerhub.databench.io/databench/nic-manager:latest
