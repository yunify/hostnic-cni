FROM alpine:edge AS build
RUN apk update
RUN apk upgrade
RUN apk add go gcc g++ make git linux-headers bash
WORKDIR /app
ENV GOPATH /app
ADD . /app/src/github.com/yunify/hostnic-cni
RUN cd /app/src/github.com/yunify/hostnic-cni && rm -rf bin/ && make

FROM alpine:latest
MAINTAINER martinyunify <martinfan@yunify.com>

COPY --from=build /app/src/github.com/yunify/hostnic-cni/bin/daemon /bin/daemon

EXPOSE 31080 31081

ENV LOGLEVEL info

ENV VXNETS vxnet-xxxxxxx

ENV POOLSIZE 3

ENV CLEANUPCACHEONEXIT false

RUN mkdir -p /opt/cni/bin/ && mkdir -p /etc/cni/net.d/ && apk --update upgrade && \
    apk add curl ca-certificates && \
    update-ca-certificates && \
    rm -rf /var/cache/apk/*

VOLUME /etc/qingcloud/

ENTRYPOINT ["/bin/daemon"]

CMD ["start"]