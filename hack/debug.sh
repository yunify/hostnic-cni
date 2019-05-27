#!/bin/bash

SKIP_BUILD=no
tag=`git rev-parse --short HEAD`
IMG=magicsong/hostnic:$tag
DEST=test/hostnic.yaml
#build binary
echo "Delete yamls before test"
kubectl delete -f $DEST > /dev/null
set -e

while [[ $# -gt 0 ]]
do
key="$1"

case $key in
    -s|--skip-build)
    SKIP_BUILD=yes
    shift # past argument
    ;;
    -t|--tag)
    tag="$2"
    shift # past argument
    shift # past value
    ;;
    --default)
    DEFAULT=YES
    shift # past argument
    ;;
    *)    # unknown option
    POSITIONAL+=("$1") # save it in an array for later
    shift # past argument
    ;;
esac
done

if [ $SKIP_BUILD == "no" ]; then
    make build-binary
    docker build -t $IMG .
	docker push $IMG
fi

echo "Generating yaml"
sed -e 's@image: .*@image: '"${IMG}"'@' deploy/hostnic.yaml > $DEST
kubectl apply -f $DEST


