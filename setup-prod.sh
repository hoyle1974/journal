# Set gcloud to prod just to be safe
gcloud config set project journal-prod-489717

# Run with the 'prod' argument
./scripts/setup-infra.sh prod
./scripts/setup-secrets.sh prod
