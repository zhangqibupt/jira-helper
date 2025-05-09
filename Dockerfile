FROM public.ecr.aws/lambda/provided:al2

# Install UVX dependencies and required packages
RUN yum update -y && \
    yum install -y gcc make openssl-devel python3 python3-pip && \
    yum clean all

# Create directory for UVX and set up Python environment
RUN mkdir -p /opt/uvx && \
    python3 -m pip install --upgrade pip && \
    python3 -m pip install uvx

# Install and cache MCP-Atlassian
RUN uvx mcp-atlassian --cache-only

# Copy the pre-built binary
COPY build/lambda/main /main

# Set the CMD to your handler
ENTRYPOINT [ "/main" ]