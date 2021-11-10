.PHONY: push
push:
	docker build -t dataworkbench/nic-manager -f images/Dockerfile .
	docker push dataworkbench/nic-manager
