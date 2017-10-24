FROM golang:1.8

WORKDIR /go/src/app
COPY main.go main.go
COPY big_buck_bunny_360p.m3u8 big_buck_bunny_360p.m3u8
COPY segments_360p segments_360p 

RUN go-wrapper download   # "go get -d -v ./..."
RUN go-wrapper install    # "go install -v ./..."

CMD ["go-wrapper", "run", "big_buck_bunny_360p.m3u8"]
