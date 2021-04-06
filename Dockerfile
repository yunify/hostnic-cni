FROM alpine
RUN apk --no-cache add ca-certificates \
    && update-ca-certificates 2>/dev/null || true
WORKDIR /app
ADD bin .
ADD scripts .
ENTRYPOINT [ "sh /app/scripts/install_hostnic.sh" ]







