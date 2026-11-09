#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
arch=amd64
output="$root/dist"
upload=
port=22

usage() {
    cat <<'USAGE'
用法
  ./scripts/package.sh [--arch amd64|arm64] [--out 目录]
                       [--upload 用户@主机:/服务器目录/] [--port SSH端口]

默认构建 Linux amd64，压缩包和 SHA-256 校验文件保存到项目的 dist/。
指定 --upload 后，使用 SCP 将两个文件上传到服务器上已存在的目录。
USAGE
}

while (($#)); do
    case "$1" in
        --help|-h) usage; exit 0 ;;
        --arch|--out|--upload|--port)
            if (($# < 2)) || [[ -z "$2" || "$2" == --* ]]; then
                printf '%s 需要填写参数\n' "$1" >&2
                exit 2
            fi
            case "$1" in
                --arch) arch=$2 ;;
                --out) output=$2 ;;
                --upload) upload=$2 ;;
                --port) port=$2 ;;
            esac
            shift 2 ;;
        *) printf '未知参数 %s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

if [[ "$arch" != amd64 && "$arch" != arm64 ]]; then
    printf '架构必须为 amd64 或 arm64\n' >&2
    exit 2
fi
if [[ ! "$port" =~ ^[1-9][0-9]{0,4}$ ]] || ((port > 65535)); then
    printf 'SSH 端口必须为 1–65535\n' >&2
    exit 2
fi
if [[ -n "$upload" && ( "$upload" != ?*:/* || "$upload" != */ || "$upload" == -* ) ]]; then
    printf '上传目标应为 用户@主机:/绝对目录/\n' >&2
    exit 2
fi

mkdir -p -- "$output"
output=$(cd "$output" && pwd)
stage=$(mktemp -d "$output/.signal-desk-package.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT

archive="signal-desk-linux-$arch.tar.gz"
(
    cd "$root"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$stage/signal-desk" ./cmd/signal-desk
)
(
    cd "$stage"
    LC_ALL=C tar -czf "$archive" signal-desk
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$archive" > "$archive.sha256"
    else
        shasum -a 256 "$archive" > "$archive.sha256"
    fi
)
mv -f -- "$stage/$archive" "$stage/$archive.sha256" "$output/"
printf '已生成 %s\n' "$output/$archive" "$output/$archive.sha256"

if [[ -n "$upload" ]]; then
    scp -P "$port" -- "$output/$archive" "$output/$archive.sha256" "$upload"
fi
