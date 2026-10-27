# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/proxykube ./cmd/proxykube

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/proxykube /proxykube
USER nonroot:nonroot
ENTRYPOINT ["/proxykube"]
CMD ["serve", "--config", "/etc/proxykube/config.yaml"]
