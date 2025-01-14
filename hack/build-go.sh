#!/usr/bin/env bash
set -e

DEST_DIR="bin"

if [ ! -d ${DEST_DIR} ]; then
	mkdir ${DEST_DIR}
fi

# Add version/commit/date into binary
# In case of TravisCI, need to check error code of 'git describe'.
if [ -z "$VERSION" ]; then
	set +e
	git describe --tags --abbrev=0 > /dev/null 2>&1
	if [ "$?" != "0" ]; then
		VERSION="master"
	else
		VERSION=$(git describe --tags --abbrev=0)
	fi
	set -e
fi
DATE=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date +%s)}" --iso-8601=seconds)
COMMIT=${COMMIT:-$(git rev-parse --verify HEAD)}
LDFLAGS="-X github.com/DataWorkbench/multus-cni/pkg/multus.version=${VERSION:-master} -X github.com/DataWorkbench/multus-cni/pkg/multus.commit=${COMMIT} -X github.com/DataWorkbench/multus-cni/pkg/multus.date=${DATE}"
export CGO_ENABLED=0

# this if... will be removed when gomodules goes default
if [ "$GO111MODULE" == "off" ]; then
	echo "Building plugin without go module"
	echo "Warning: this will be deprecated in near future so please use go modules!"

	ORG_PATH="gopkg.in/k8snetworkplumbingwg"
	REPO_PATH="${ORG_PATH}/multus-cni.v3"

	if [ ! -h gopath/src/${REPO_PATH} ]; then
		mkdir -p gopath/src/${ORG_PATH}
		ln -s ../../../.. gopath/src/${REPO_PATH} || exit 255
	fi

	export GO15VENDOREXPERIMENT=1
	export GOBIN=${PWD}/bin
	export GOPATH=${PWD}/gopath
	go build -o ${PWD}/bin/multus -tags no_openssl -ldflags "${LDFLAGS}" "$@" ${REPO_PATH}/cmd
	go build -o ${PWD}/bin/multus-repair-vips -tags no_openssl -ldflags "${LDFLAGS}" "$@" ${REPO_PATH}/cmd/repair-vips
	go build -o ${PWD}/bin/generate-kubeconfig -tags no_openssl -ldflags "${LDFLAGS}" ${REPO_PATH}/cmd/config-generation
	go build -o ${PWD}/bin/multus-daemon -tags no_openssl -ldflags "${LDFLAGS}" "$@" ${REPO_PATH}/cmd/controller/
	go build -o ${PWD}/bin/multus-nic-manager -tags no_openssl -ldflags "${LDFLAGS}" "$@" ${REPO_PATH}/cmd/nic-manager/
else
	# build with go modules
	export GO111MODULE=on
	BUILD_ARGS=(-o ${DEST_DIR}/multus -tags no_openssl)
	if [ -n "$MODMODE" ]; then
		BUILD_ARGS+=(-mod "$MODMODE")
	fi
	export CGO_ENABLED=0 GOOS=linux GOARCH=amd64
	echo "Building plugins"
	go build ${BUILD_ARGS[*]} -ldflags "${LDFLAGS}" "$@" ./cmd
	echo "Building repair vips"
	go build -o "${DEST_DIR}"/multus-repair-vips -ldflags "${LDFLAGS}" ./cmd/repair-vips
	echo "Building spec generators"
	go build -o "${DEST_DIR}"/generate-kubeconfig -ldflags "${LDFLAGS}" ./cmd/config-generation
	echo "Building multus controller"
	go build -o "${DEST_DIR}"/multus-daemon -ldflags "${LDFLAGS}" ./cmd/controller/
	echo "Building multus nic-manager"
	go build -o "${DEST_DIR}"/multus-nic-manager -ldflags "${LDFLAGS}" ./cmd/nic-manager/
	echo "Building multus nic-device-plugin"
	go build -o "${DEST_DIR}"/multus-nic-device-plugin -ldflags "${LDFLAGS}" ./cmd/device-plugin/

fi
