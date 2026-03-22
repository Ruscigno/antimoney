#!/bin/bash
set -e

echo "============================================="
echo " Deploying Antimoney to Google Cloud"
echo "============================================="

# Load .env file if it exists
if [ -f ".env" ]; then
  export $(grep -v '^#' .env | xargs)
fi

# Ensure variables exist
if [ -z "$PROJECT_ID" ]; then
  echo "Error: PROJECT_ID environment variable is not set."
  echo "Usage: PROJECT_ID=my-project-id ./deploy.sh or add it to your .env file"
  exit 1
fi

export REGION="us-central1"
export REPO_URL="us-central1-docker.pkg.dev/$PROJECT_ID/antimoney-repo"

echo "[1/4] Applying Terraform Infrastructure..."
cd infra/
terraform init
terraform apply -var="project_id=${PROJECT_ID}" -auto-approve
cd ../

# Get the provisioned URLs from Terraform outputs
BACKEND_URL=$(cd infra && terraform output -raw backend_url)
FRONTEND_URL=$(cd infra && terraform output -raw frontend_url)
STAGING_BUCKET=$(cd infra && terraform output -raw build_staging_bucket)

echo "[2/5] Waiting 90s for Database VM startup script to finish installing PostgreSQL..."
# (A typical ubuntu package install + docker pull takes ~60-80s on an e2-micro)
sleep 90

echo "[3/5] Removing Public IP from Database VM to avoid $3.60/month charge..."
export ZONE="${REGION}-a"
ACCESS_CONFIG_NAME=$(gcloud compute instances describe antimoney-db --zone=$ZONE --format="value(networkInterfaces[0].accessConfigs[0].name)")
if [ ! -z "$ACCESS_CONFIG_NAME" ]; then
    gcloud compute instances delete-access-config antimoney-db \
        --zone=$ZONE \
        --access-config-name="$ACCESS_CONFIG_NAME" \
        --quiet
    echo "Removed Public IP ($ACCESS_CONFIG_NAME) successfully. Database is now 100% free and fully private."
else
    echo "No Public IP found on the Database VM (perhaps already removed?)."
fi

echo "[4/5] Building and Deploying Backend to Cloud Run..."
cd backend/
# Build and push to our explicit Artifact Registry repo
gcloud builds submit --tag $REPO_URL/backend:latest . --gcs-source-staging-dir gs://$STAGING_BUCKET/backend

gcloud run deploy antimoney-backend \
  --image $REPO_URL/backend:latest \
  --project $PROJECT_ID \
  --region $REGION \
  --quiet

echo "[5/5] Building and Deploying Frontend to Cloud Run..."
cd ../frontend/
# Cloud Build looks for "Dockerfile" by default. We swap them temporarily.
mv Dockerfile Dockerfile.dev
mv Dockerfile.prod Dockerfile

gcloud builds submit --tag $REPO_URL/frontend:latest . --gcs-source-staging-dir gs://$STAGING_BUCKET/frontend || {
  mv Dockerfile Dockerfile.prod
  mv Dockerfile.dev Dockerfile
  exit 1
}

# Restore original Dockerfiles
mv Dockerfile Dockerfile.prod
mv Dockerfile.dev Dockerfile

gcloud run deploy antimoney-frontend \
  --image $REPO_URL/frontend:latest \
  --project $PROJECT_ID \
  --region $REGION \
  --quiet

echo "============================================="
echo " 🎉 Deployment Complete! 🎉"
echo "============================================="
echo " Backend URL: $BACKEND_URL"
echo " Frontend URL (Your App!): $FRONTEND_URL"
echo "============================================="
