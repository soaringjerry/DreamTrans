#!/bin/bash

echo "=== DreamTrans PCAS Provider Build Verification ==="
echo

# Navigate to backend directory
cd "$(dirname "$0")"

echo "1. Running go mod tidy to clean up dependencies..."
go mod tidy

echo
echo "2. Building PCAS Provider..."
cd cmd/pcas-provider
go build -v

if [ $? -eq 0 ]; then
    echo
    echo "✅ Build successful! PCAS Provider is ready."
    echo
    echo "3. Building Web service to ensure it still works..."
    cd ../web
    go build -v
    
    if [ $? -eq 0 ]; then
        echo
        echo "✅ Web service build successful!"
    else
        echo
        echo "❌ Web service build failed!"
        exit 1
    fi
else
    echo
    echo "❌ PCAS Provider build failed!"
    exit 1
fi

echo
echo "=== All builds completed successfully! ==="