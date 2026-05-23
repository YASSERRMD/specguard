#!/usr/bin/env bash
set -euo pipefail

API_KEY="my_secure_api_key"
SERVER_URL="http://localhost:8090"

echo "Waiting for Specguard server to be healthy at ${SERVER_URL}..."
for i in {1..30}; do
  if curl -s -f "${SERVER_URL}/health" >/dev/null; then
    echo "Specguard server is healthy!"
    break
  fi
  sleep 1
done

if ! curl -s -f "${SERVER_URL}/health" >/dev/null; then
  echo "Error: Specguard server did not become healthy in time."
  exit 1
fi

echo "Adding REST spec (Product Catalog)..."
REST_RAW=$(jq -Rs . docs/specs/rest_spec.yaml)
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"id\": \"rest-store\", \"raw\": ${REST_RAW}}" \
  "${SERVER_URL}/api/specs"
echo "REST spec added successfully."

echo "Adding gRPC spec (User Service)..."
GRPC_RAW=$(jq -Rs . docs/specs/grpc_spec.proto)
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"id\": \"grpc-users\", \"raw\": ${GRPC_RAW}}" \
  "${SERVER_URL}/api/specs"
echo "gRPC spec added successfully."

echo "Configuring REST mock to run on port 8081..."
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"id": "rest-store", "config": {"host": "0.0.0.0", "port": 8081}}' \
  "${SERVER_URL}/api/mocks/config"
echo "REST mock configured."

echo "Configuring gRPC mock to run on port 8082..."
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"id": "grpc-users", "config": {"host": "0.0.0.0", "port": 8082}}' \
  "${SERVER_URL}/api/mocks/config"
echo "gRPC mock configured."

echo "Starting REST mock server..."
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"id": "rest-store"}' \
  "${SERVER_URL}/api/mocks/start"
echo "REST mock server started."

echo "Starting gRPC mock server..."
curl -f -s -X POST \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"id": "grpc-users"}' \
  "${SERVER_URL}/api/mocks/start"
echo "gRPC mock server started."

echo "Data seeded and mock servers started successfully!"
echo "REST Mock Server listening at http://localhost:8081"
echo "gRPC Mock Server listening at localhost:8082"
