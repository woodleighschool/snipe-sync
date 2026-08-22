# syntax=docker/dockerfile:1

# Keep the container toolchain aligned with Mise. Renovate updates both.

# ---- Go build -------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN apk add --no-cache upx
WORKDIR /workspace

# Cache module downloads before copying source.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o snipe-sync ./cmd/snipe-sync
RUN upx --best --lzma snipe-sync

# ---- Runtime --------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/snipe-sync /snipe-sync
USER 65532:65532
ENTRYPOINT ["/snipe-sync"]
CMD ["run", "--once"]
