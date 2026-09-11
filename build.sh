#!/usr/bin/env bash
# Build and push the image to Docker Hub. The account, image name and
# target platform all come from environment variables — nothing about the
# registry account is hardcoded in this script.
#
#   export DOCKERHUB_USER=<your Docker Hub account>
#   ./build.sh
set -euo pipefail

: "${DOCKERHUB_USER:?export DOCKERHUB_USER=<your Docker Hub account> first}"

IMAGE_NAME="${IMAGE_NAME:-ks26-app}"
# Tag is derived from the commit SHA so it is always unique; never "latest",
# or repeated tags let stale node-cached images pass for the new build.
IMAGE_TAG="${IMAGE_TAG:-$(git rev-parse --short=7 HEAD)}"
# Target platform depends on the cluster's node architecture — override
# this default when building for your own environment.
PLATFORM="${PLATFORM:-linux/amd64}"

# Registry is always spelled out explicitly; never rely on the docker CLI's
# implicit default registry.
IMAGE="docker.io/${DOCKERHUB_USER}/${IMAGE_NAME}:${IMAGE_TAG}"

echo "building ${IMAGE} for ${PLATFORM}"

docker buildx build \
  --platform "${PLATFORM}" \
  --tag "${IMAGE}" \
  --push \
  .

echo
echo "pushed: ${IMAGE}"
echo "copy this tag into deploy/deployment.yaml's image and deploy/configmap.yaml's IMAGE_TAG."
