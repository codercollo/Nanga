#!/usr/bin/env bash
set -euo pipefail

echo "=========================================="
echo "Scaffolding Nanga Project Structure..."
echo "=========================================="

# 1. Create Command (Entrypoints) Directories & Files
echo "-> Creating cmd/ entries..."
mkdir -p cmd/gateway
mkdir -p cmd/identity
mkdir -p cmd/ledger
mkdir -p cmd/payments
mkdir -p cmd/mpesa
mkdir -p cmd/worker

touch cmd/gateway/main.go
touch cmd/identity/main.go
touch cmd/ledger/main.go
touch cmd/payments/main.go
touch cmd/mpesa/main.go
touch cmd/worker/main.go


# 2. Create Internal Packages & Hexagonal Layers
echo "-> Creating internal/ packages..."
mkdir -p internal/gateway/http
mkdir -p internal/gateway/client

# Identity module
mkdir -p internal/identity/domain
mkdir -p internal/identity/app
mkdir -p internal/identity/adapter/grpc
mkdir -p internal/identity/adapter/postgres

# Ledger module
mkdir -p internal/ledger/domain
mkdir -p internal/ledger/app
mkdir -p internal/ledger/adapter/grpc
mkdir -p internal/ledger/adapter/postgres

# Payments module
mkdir -p internal/payments/domain
mkdir -p internal/payments/app
mkdir -p internal/payments/adapter/grpc
mkdir -p internal/payments/adapter/postgres
mkdir -p internal/payments/adapter/redis
mkdir -p internal/payments/adapter/rabbitmq

# M-Pesa module
mkdir -p internal/mpesa/domain
mkdir -p internal/mpesa/app
mkdir -p internal/mpesa/adapter/grpc
mkdir -p internal/mpesa/adapter/http
mkdir -p internal/mpesa/adapter/daraja
mkdir -p internal/mpesa/adapter/redis
mkdir -p internal/mpesa/adapter/postgres

# Worker module
mkdir -p internal/worker/notify
mkdir -p internal/worker/recon

# Platform shared building blocks
PLATFORMS=(money config obs pg redisx rmq outbox idempotency authn grpcx)
for p in "${PLATFORMS[@]}"; do
  mkdir -p "internal/platform/$p"
done


# 3. Create Protobuf and Generated Code Directories
echo "-> Creating proto/ and gen/ directories..."
mkdir -p proto/nanga/identity/v1
mkdir -p proto/nanga/ledger/v1
mkdir -p proto/nanga/payments/v1
mkdir -p proto/nanga/mpesa/v1
mkdir -p proto/nanga/events/v1

touch proto/buf.yaml
touch proto/buf.gen.yaml
touch proto/nanga/identity/v1/identity.proto
touch proto/nanga/ledger/v1/ledger.proto
touch proto/nanga/payments/v1/payments.proto
touch proto/nanga/mpesa/v1/mpesa.proto
touch proto/nanga/events/v1/events.proto

mkdir -p gen


# 4. Create Database Migrations and Queries
echo "-> Creating db/ migrations and queries..."
SCHEMAS=(identity ledger payments mpesa recon)
for s in "${SCHEMAS[@]}"; do
  mkdir -p "db/migrations/$s"
  mkdir -p "db/queries/$s"
done


# 5. Create Deployment and Orchestration Folders
echo "-> Creating deploy/ and workflow directories..."
mkdir -p deploy/k8s/base
mkdir -p deploy/k8s/overlays/staging
mkdir -p deploy/k8s/overlays/production

touch deploy/Dockerfile
touch deploy/docker-compose.yaml


# 6. Create Test & Simulation Folders
echo "-> Creating test/ directories..."
mkdir -p test/e2e
mkdir -p test/daraja-sim
touch test/e2e/.gitkeep
touch test/daraja-sim/.gitkeep


# 7. Create CI/CD Workflows
mkdir -p .github/workflows
touch .github/workflows/ci.yml
touch .github/workflows/release.yml


# 8. Create Root Configuration & Documentation Files
echo "-> Creating root configuration files..."
touch .env.example
touch .gitignore
touch .golangci.yml
touch go.mod
touch go.sum
touch Makefile
touch sqlc.yaml
touch README.md
touch spec.md
touch project-structure.md

echo "=========================================="
echo "Nanga scaffolding successfully completed! 🚀"
echo "=========================================="