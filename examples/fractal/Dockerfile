# build.sh compiles the server before this runs. The build sandbox cannot
# start a container, so the image can have no RUN step.
FROM scratch
COPY server /server
COPY web /fractal/web
COPY *.fern /fractal/
COPY *.fern.md /fractal/
COPY .showcase /showcase
WORKDIR /
EXPOSE 8080
CMD ["/server", "."]
