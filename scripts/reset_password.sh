#!/bin/bash
set -e

# Move to the project root directory
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR/.."

# Load .env file to get PROJECT_ID if exists
if [ -f ".env" ]; then
  export $(grep -v '^#' .env | xargs)
fi

if [ -z "$PROJECT_ID" ]; then
  echo "Error: PROJECT_ID environment variable is not set."
  echo "Make sure it is set in your .env file."
  exit 1
fi

read -p "Enter user email: " USER_EMAIL
read -s -p "Enter new password: " NEW_PASSWORD
echo ""

# We use the pgcrypto extension's crypt() function to generate a bcrypt hash natively in Postgres
# The second parameter gen_salt('bf', 10) ensures it generates a hash compatible with Go's bcrypt.DefaultCost (10)
echo "Running update query on the production database..."

gcloud compute ssh antimoney-db --zone=us-central1-a --project="${PROJECT_ID}" --command="sudo docker exec -i postgres psql -U antimoney -d antimoney" <<EOF
UPDATE users 
SET password_hash = crypt('${NEW_PASSWORD}', gen_salt('bf', 10)) 
WHERE email = '${USER_EMAIL}';
EOF

echo "Done. The password has been updated securely."
