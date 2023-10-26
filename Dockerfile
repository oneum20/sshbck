FROM golang:1.19.13-alpine3.18 as prod
LABEL maintainer="github.com/oneum20"

RUN mkdir /app

WORKDIR /app
COPY sshbck /app
ENTRYPOINT [ "/app/sshbck" ]
