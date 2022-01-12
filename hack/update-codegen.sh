#!/bin/bash -e

set -x

realpath() {
    [[ $1 = /* ]] && echo "$1" || echo "$PWD/${1#./}"
}

# set the passed in directory as a usable GOPATH
# that deepcopy-gen can operate in
ensure-temp-gopath() {
	fake_gopath=$1

	# set up symlink pointing to our repo root
	fake_repopath=$fake_gopath/src/github.com/AliyunContainerService/ack-secret-manager
	mkdir -p "$(dirname "${fake_repopath}")"
	ln -s "$REPO_FULL_PATH" "${fake_repopath}"
}

SCRIPT_ROOT=$(dirname ${BASH_SOURCE})/..
REPO_FULL_PATH=$(realpath ${SCRIPT_ROOT})
cd ${REPO_FULL_PATH}

CODEGEN_PKG=${CODEGEN_PKG:-$(cd ${SCRIPT_ROOT}; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ../../../k8s.io/code-generator)}

verify="${VERIFY:-}"

valid_gopath=$(realpath $REPO_FULL_PATH/../../../..)
if [[ "$(realpath ${valid_gopath}/src/github.com/AliyunContainerService/ack-secret-manager)" == "${REPO_FULL_PATH}" ]]; then
	temp_gopath=${valid_gopath}
else
	TMP_DIR=$(mktemp -d -t ack-secret-manager-codegen.XXXX)
	function finish {
		chmod -R +w ${TMP_DIR}
		# ok b/c we will symlink to the original repo
		rm -r ${TMP_DIR}
	}
	trap finish EXIT

	ensure-temp-gopath ${TMP_DIR}

	temp_gopath=${TMP_DIR}
fi

GOPATH="${temp_gopath}" GOFLAGS="" bash ${CODEGEN_PKG}/generate-groups.sh "deepcopy" \
	github.com/AliyunContainerService/ack-secret-manager/pkg/client \
	github.com/AliyunContainerService/ack-secret-manager/pkg/apis \
	"alibabacloud:v1alpha1" \
	--go-header-file ${REPO_FULL_PATH}/hack/boilerplate.go.txt \
	${verify}
