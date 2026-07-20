FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/regstair ./cmd/regstair
RUN mkdir -p /out/regstair-content

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/regstair /regstair
COPY --from=build --chown=65532:65532 /out/regstair-content /var/lib/regstair/content
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/regstair"]
