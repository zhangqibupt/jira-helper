#!/bin/bash

# Set environment variables
export UV_CACHE_DIR=/tmp/uvx-cache
export UV_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple

# Create cache directory if it doesn't exist
mkdir -p $UV_CACHE_DIR

# Create a virtual environment
echo "Creating virtual environment..."
uv venv /tmp/uvx-venv

# Activate the virtual environment
source /tmp/uvx-venv/bin/activate

# List of commonly used packages
PACKAGES=(
    "requests"
    "urllib3"
    "certifi"
    "charset-normalizer"
    "idna"
    "python-dateutil"
    "pytz"
    "six"
    "tzdata"
)

# Pre-download packages
for package in "${PACKAGES[@]}"; do
    echo "Pre-caching package: $package"
    uv pip install --no-deps $package
done

# Deactivate virtual environment
deactivate

echo "Package pre-caching completed!" 