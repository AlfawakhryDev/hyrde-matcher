# Build stage: compile a static binary. CGO off means no C library needed at
# runtime, so the final image can be almost empty.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE=matcher
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

# Runtime stage: distroless has no shell and no package manager, which leaves
# an attacker very little to work with. ~15 MB in total.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
# Numeric user and group, so Kubernetes can verify runAsNonRoot.
USER 65532:65532
ENTRYPOINT ["/app"]
