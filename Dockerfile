FROM node:22-alpine AS frontend

WORKDIR /src
COPY package.json package-lock.json tsconfig.json tsconfig.app.json tsconfig.node.json vite.config.ts ./
COPY frontend ./frontend
RUN npm ci
RUN npm test && npm run build

FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/internal/admin/frontend-dist ./internal/admin/frontend-dist
RUN CGO_ENABLED=0 go build -o /out/regstair ./cmd/regstair
RUN mkdir -p /out/regstair-content /out/regstair-tls /out/regstair-credentials

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/regstair /regstair
COPY --from=build --chown=65532:65532 /out/regstair-content /var/lib/regstair/content
COPY --from=build --chown=65532:65532 /out/regstair-tls /var/lib/regstair/tls
COPY --from=build --chown=65532:65532 /out/regstair-credentials /var/lib/regstair/credentials
USER nonroot:nonroot
EXPOSE 80 443
ENTRYPOINT ["/regstair"]
