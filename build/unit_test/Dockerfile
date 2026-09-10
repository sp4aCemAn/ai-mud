# syntax=docker/dockerfile:1.4
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /src
ENV CGO_ENABLED 0
ENV GOPATH /go
ENV GOCACHE /go-build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod/cache \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go-build \
    go build -o /bin/unit_test ./cmd/app/unit_test

# stage 2

FROM scratch

COPY --from=builder /bin/unit_test /unit_test

EXPOSE 8080 2222

CMD [ "/unit_test" ]
