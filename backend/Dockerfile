FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app . && CGO_ENABLED=0 go build -o /sensorsim ./cmd/sensorsim

# Sensor simulator (docker build --target sim): publishes recorded readings to Kafka as if live.
FROM gcr.io/distroless/static AS sim
COPY --from=build /sensorsim /sensorsim
ENTRYPOINT ["/sensorsim"]

# The API service (default target).
FROM gcr.io/distroless/static
COPY --from=build /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]