# install kustomize

curl -s "https://raw.githubusercontent.com/\
kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash
cp kustomize /usr/local/bin/

# install protobuf
go get github.com/gogo/protobuf/protoc-gen-gofast
apt install -y protobuf-compiler