FROM alpine:3.22 AS builder

RUN apk --no-cache add go

ADD . /code
WORKDIR /code

RUN go build

RUN chmod +x /code/corsanywhere


FROM alpine:3.22

COPY --from=builder /code/corsanywhere /usr/bin/corsanywhere

CMD ["corsanywhere"]
