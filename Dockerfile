FROM alpine
RUN apk --no-cache add iptables
WORKDIR /app

ADD bin .
ADD config .
ADD scripts .

ENTRYPOINT [ "/app/install_hostnic.sh" ]







