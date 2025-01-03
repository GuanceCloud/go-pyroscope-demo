FROM golang:1.19-bullseye

LABEL authors="guance.com" \
    email="zhangyi905@guance.com"

COPY . /usr/local/go-pyroscope-demo
WORKDIR /usr/local/go-pyroscope-demo

RUN go env -w GOPROXY="https://goproxy.cn,direct" && go build

ENV DD_SERVICE go-pyroscope-demo
ENV DD_VERSION v0.1.0
ENV DD_ENV testing
ENV DD_AGENT_HOST 127.0.0.1
ENV DD_TRACE_AGENT_PORT 9529
ENV DD_TRACE_ENABLED true
ENV DD_PROFILING_ENABLED true

CMD ./go-pyroscope-demo
