ARG BASE_IMAGE=alpine:3.20
FROM ${BASE_IMAGE} AS builder
RUN echo "artifact" > /artifact.txt
FROM nginx:alpine AS runtime
COPY --from=builder /artifact.txt /usr/share/nginx/html/artifact.txt
