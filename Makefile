.PHONY: push
push:
	docker build -t dockerhub.dataomnis.io/dataomnis/nic-manager:latest -f images/Dockerfile .
	docker push dockerhub.dataomnis.io/dataomnis/nic-manager:latest
