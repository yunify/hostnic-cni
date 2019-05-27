FROM alpine
RUN apk --no-cache add iptables ca-certificates \
    && update-ca-certificates 2>/dev/null || true
WORKDIR /app

ADD bin .
ADD config .
ADD scripts .

ENTRYPOINT [ "/app/install_hostnic.sh" ]







