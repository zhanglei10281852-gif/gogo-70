# Multi-stage build for the offline CableMend CLI.
#
# The builder stage runs entirely offline: the module has no dependencies, the
# Go toolchain is pinned to the image toolchain (GOTOOLCHAIN=local) and the
# module proxy is disabled (GOPROXY=off). The final stage contains nothing but
# the static binary.
FROM golang:1.22 AS builder

ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    GOPROXY=off \
    GOFLAGS=-mod=mod

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN go build -trimpath -ldflags "-s -w" -o /out/cablemend ./cmd/cablemend

FROM scratch

COPY --from=builder /out/cablemend /cablemend

ENTRYPOINT ["/cablemend"]
