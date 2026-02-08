FROM golang:1.23-alpine AS builder

RUN apk add --no-cache build-base opus-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o main .

FROM alpine:latest

RUN apk add --no-cache espeak-ng opus

WORKDIR /app

COPY --from=builder /app/main .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

EXPOSE 8080

CMD ["./main"]
