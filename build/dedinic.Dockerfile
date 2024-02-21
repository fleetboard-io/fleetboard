FROM  airren/kube-ovn-base:v1.13.0
WORKDIR /dedinic
COPY ../cmd/dedinic/dedinic .
COPY ../build/start-dedinic.sh .
