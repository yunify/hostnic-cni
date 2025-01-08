FROM alpine
RUN apk --no-cache add ca-certificates \
    && apk --no-cache add ebtables \
    && apk --no-cache add ipvsadm \
    && update-ca-certificates 2>/dev/null || true
WORKDIR /app
ADD bin/hostnic .
ADD scripts .
ENTRYPOINT [ "sh /app/scripts/install_hostnic.sh" ]
