FROM scratch
COPY cmd/bot /bot
ENTRYPOINT [ "/bot" ]