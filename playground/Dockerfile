FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /playground-backend ./playground/backend/

FROM golang:1.25-alpine
RUN apk add --no-cache ca-certificates
COPY --from=builder /playground-backend /usr/local/bin/playground-backend
ENV PORT=8081
EXPOSE 8081
CMD ["playground-backend"]
