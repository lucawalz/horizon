FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X github.com/lucawalz/horizon/internal/version.version=${VERSION}" \
    -o /out/horizon ./cmd/horizon

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /src/LICENSE /licenses/LICENSE
COPY --from=build /out/horizon /usr/local/bin/horizon

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/horizon"]
