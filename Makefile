# ==============================================================================
# Variables

# Name of the executable
APP_NAME := myapp
# Directory to output the compiled binary
BUILD_DIR := ./bin
# Entry point of the application
MAIN_FILE := ./cmd/app/server/main.go

# ==============================================================================
# Targets

# The .PHONY directive tells Make that these targets don't represent actual files
.PHONY: all build run test clean fmt lint tidy help

# Default target when you just run `make`
all: build

# Build the application
build:
	@echo "==> Building $(APP_NAME)..."
	@go build -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_FILE)

# Run the application
run: build
	@echo "==> Running $(APP_NAME)..."
	@$(BUILD_DIR)/$(APP_NAME)

# Run tests
test:
	@echo "==> Running tests..."
	@go test ./... -v

# Clean up built binaries
clean:
	@echo "==> Cleaning build directory..."
	@rm -rf $(BUILD_DIR)

# Format the code
fmt:
	@echo "==> Formatting code..."
	@go fmt ./...

# Run the linter (requires golangci-lint)
lint:
	@echo "==> Running linter..."
	@golangci-lint run

# Tidy up the go.mod and go.sum files
tidy:
	@echo "==> Tidying module dependencies..."
	@go mod tidy

# Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build    Compile the Go application"
	@echo "  run      Compile and run the Go application"
	@echo "  test     Run all tests in the project"
	@echo "  clean    Remove the built binary"
	@echo "  fmt      Format all Go code in the project"
	@echo "  lint     Run golangci-lint (must be installed)"
	@echo "  tidy     Tidy go.mod and go.sum"
