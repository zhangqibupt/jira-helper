FROM public.ecr.aws/lambda/python:3.10
ENV UV_CACHE_DIR=/var/task/uvx-cache
ENV UV_TOOL_DIR=/var/task/uvx-tool

# 安装编译工具链
RUN yum update -y && \
yum install -y gcc gcc-c++ make gcc-aarch64-linux-gnu gcc-c++-aarch64-linux-gnu


# Create the cache directories and set permissions
RUN mkdir -p /var/task/uvx-cache /var/task/uvx-tool && \
    chmod -R 777 /var/task/uvx-cache /var/task/uvx-tool

    # 安装 uvx 和缓存（使用预编译的二进制包）
RUN set -x && \
    python3 -m pip install --upgrade pip && \
    python3 -m pip install --only-binary :all: -i https://pypi.tuna.tsinghua.edu.cn/simple uvx && \
    # 预先下载和缓存 mcp-atlassian 
    uvx install mcp-atlassian@0.11.1

# 复制主程序
COPY build/lambda/main /main
RUN chmod +x /main

ENTRYPOINT ["/main"]