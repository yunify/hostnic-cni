FROM alpine:latest
MAINTAINER martinyunify <martinfan@yunify.com>

ADD daemon /



ENTRYPOINT ["/daemon"]