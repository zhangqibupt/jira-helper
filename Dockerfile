FROM public.ecr.aws/lambda/python:3.9

# 安装编译工具链
RUN yum update -y && \
    yum install -y gcc gcc-c++ make gcc-aarch64-linux-gnu gcc-c++-aarch64-linux-gnu

# 安装 uvx 和缓存（使用预编译的二进制包）
RUN python3 -m pip install --upgrade pip && \
    python3 -m pip install --only-binary :all: -i https://pypi.tuna.tsinghua.edu.cn/simple uvx && \
    uvx mcp-atlassian

# 复制主程序
COPY build/lambda/main /main
RUN chmod +x /main

ENTRYPOINT ["/main"]