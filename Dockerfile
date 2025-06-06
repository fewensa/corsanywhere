FROM alpine:3.22 AS builder

RUN apk --no-cache add go

COPY go.mod go.sum /code/
WORKDIR /code
RUN go mod download
COPY . /code
RUN go build

RUN chmod +x /code/corsanywhere


FROM alpine:3.22

COPY --from=builder /code/corsanywhere /usr/bin/corsanywhere

CMD ["corsanywhere"]
