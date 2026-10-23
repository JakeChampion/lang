# build.sh compiles the server before this runs. The Netlify build sandbox
# cannot start a container, so the image can have no RUN step.
FROM scratch
COPY server /server
COPY web /web
WORKDIR /
EXPOSE 8080
CMD ["/server"]
