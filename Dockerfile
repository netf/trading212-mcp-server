# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/trading212-mcp-server ./cmd/trading212-mcp-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/trading212-mcp-server /usr/local/bin/trading212-mcp-server
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/trading212-mcp-server"]
