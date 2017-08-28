FROM alpine:latest
MAINTAINER martinyunify <martinfan@yunify.com>

RUN mkdir -p /opt/cni/bin/ && mkdir -p /etc/cni/net.d/

EXPOSE 31080 31081

ENV LOGLEVEL info

ENV VXNETS vxnet-xxxxxxx

ENV POOLSIZE 3

ENV CLEANUPCACHEONEXIT false

ADD daemon /

VOLUME /etc/qingcloud/

VOLUME /etc/cni/net.d/

ADD hostnic /opt/cni/bin/

ENTRYPOINT ["/daemon"]