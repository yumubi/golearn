FROM alpine:3.22
COPY fakeup /usr/local/bin/fakeup
ENTRYPOINT ["/usr/local/bin/fakeup"]
