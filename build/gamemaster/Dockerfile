# syntax=docker/dockerfile:1.4
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src
ENV CGO_ENABLED 0
ENV GOPATH /go
ENV GOCACHE /go-build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod/cache \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go-build \
    go build -o /bin/server ./cmd/app/server 

# stage 2

FROM scratch

COPY --from=builder /bin/server /server

EXPOSE 8080 2222

# start the game master
CMD [ "/server" ]
