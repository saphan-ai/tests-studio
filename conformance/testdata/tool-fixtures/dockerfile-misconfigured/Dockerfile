FROM alpine:3.19
ADD https://example.com/releases/app.tar.gz /opt/app.tar.gz
RUN tar -xzf /opt/app.tar.gz -C /opt
CMD ["/opt/app/run"]
