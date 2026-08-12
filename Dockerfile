FROM golang:1.26.5-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/pyttechat ./cmd/pyttechat

FROM alpine:3.23

RUN addgroup -S pyttechat && adduser -S -G pyttechat -h /var/lib/pyttechat pyttechat
RUN mkdir -p /var/lib/pyttechat && chown -R pyttechat:pyttechat /var/lib/pyttechat

COPY --from=builder /out/pyttechat /usr/local/bin/pyttechat

ENV PYTTECHAT_DATABASE_URL=sqlite:///var/lib/pyttechat/pyttechat.db
ENV PYTTECHAT_REGISTRATION_ENABLED=true
ENV PYTTECHAT_WEB_ADDR=:3000

USER pyttechat
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/pyttechat"]
CMD ["serve", "--addr", ":3000"]
