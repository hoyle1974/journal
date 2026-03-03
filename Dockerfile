# Runtime-only image - binary is pre-built by deploy script
FROM gcr.io/distroless/static-debian12:nonroot

# Copy ca-certificates (needed for HTTPS calls)
COPY --chmod=644 ca-certificates.crt /etc/ssl/certs/

# Copy the pre-built binary
COPY --chmod=755 server /server

# Run as non-root user
USER nonroot:nonroot

# Cloud Run uses PORT env var
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/server"]
