# Build the bellu CLI into a small runtime image.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/bellu ./cmd/bellu

FROM alpine:3.20
RUN adduser -D -u 10001 bellu
WORKDIR /app
COPY --from=build /out/bellu /usr/local/bin/bellu
COPY bellu.yaml /app/bellu.yaml
USER bellu
ENTRYPOINT ["bellu"]
