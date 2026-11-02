#!/bin/sh
set -eu
base_url='@OXGEN_BASE_URL@'
install_dir=''
download_dir=''
no_path=0
no_launch=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --base-url) base_url=$2; shift 2 ;;
    --home) install_dir=$2; shift 2 ;;
    --download-dir) download_dir=$2; shift 2 ;;
    --no-path) no_path=1; shift ;;
    --no-launch) no_launch=1; shift ;;
    *) printf '未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done
case "$base_url" in https://*) ;; *) printf '域名待定，请传入 --base-url https://你的域名/oxgen/\n' >&2; exit 2 ;; esac
case "$base_url" in *'@'*|*'?'*|*'#'*|*' '*|*'\'*) printf '下载地址格式无效\n' >&2; exit 2 ;; esac
base_url=${base_url%/}
github_release=0
case "$base_url" in
  https://github.com/*/releases/latest/download|https://github.com/*/releases/download/v*) github_release=1 ;;
esac
case "$(uname -s)" in Darwin) platform=darwin ;; Linux) platform=linux ;; *) printf '首版支持 macOS、Ubuntu、Debian\n' >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) printf '不支持的 CPU 架构\n' >&2; exit 2 ;; esac
if [ "$platform" = linux ]; then
  distro=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
  case "$distro" in ubuntu|debian) ;; *) printf 'Linux 首版只支持 Ubuntu／Debian\n' >&2; exit 2 ;; esac
fi
fetch() {
  if command -v curl >/dev/null 2>&1; then
    redirects=0; [ "$github_release" = 0 ] || redirects=5
    curl --fail --location --max-redirs "$redirects" --proto '=https' --proto-redir '=https' --connect-timeout 30 --max-time 600 "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    redirects=0; [ "$github_release" = 0 ] || redirects=5
    wget --https-only --max-redirect="$redirects" --timeout=60 -O "$2" "$1"
  else printf '首次下载需要系统已有 curl 或 wget；不需要 Python、Node.js 或 Go。\n' >&2; exit 1
  fi
}
ask() { printf '%s' "$1" >/dev/tty; IFS= read -r answer </dev/tty; }
printf '\nOXGEN 氧气邮政员 · 安装工具箱\n'
if [ -z "$install_dir" ]; then ask "粘贴 OXGEN 安装目录，0 返回；回车使用 $HOME/.oxgen："; [ "$answer" = 0 ] && exit 0; install_dir=${answer:-"$HOME/.oxgen"}; fi
if [ -z "$download_dir" ]; then download_dir="$install_dir/downloads"; fi
for directory in "$install_dir" "$download_dir"; do
  case "$directory" in /*) ;; *) printf '请输入完整绝对路径\n' >&2; exit 2 ;; esac
  case "$directory" in /|*'
'*) printf '请选择具体文件夹，目录不能包含换行\n' >&2; exit 2 ;; esac
done
printf '下载目录：%s\n最终程序：%s/bin/oxgen\n' "$download_dir" "$install_dir"
ask '输入 1 开始安装，输入 0 返回：'; [ "$answer" = 1 ] || exit 0
mkdir -p "$install_dir" "$download_dir"
stage=$(mktemp -d "$download_dir/.oxgen-bootstrap.XXXXXX")
cleanup() {
  case "$stage" in "$download_dir"/.oxgen-bootstrap.*) [ -d "$stage" ] && rm -rf -- "$stage" ;; esac
}
trap cleanup EXIT HUP INT TERM
fetch "$base_url/manifest.tsv" "$stage/manifest.tsv"
header=$(head -n 1 "$stage/manifest.tsv")
case "$header" in '# oxgen-manifest-v1'*) ;; *) printf '不支持的发布清单\n' >&2; exit 1 ;; esac
if [ "$github_release" = 1 ]; then
  version=$(printf '%s\n' "$header" | awk -F '\t' '{print $2}')
  if ! printf '%s\n' "$version" | awk '/^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$/ {valid=1} END{exit !valid}'; then printf 'GitHub 发行版本无效\n' >&2; exit 1; fi
  repo_root=${base_url%%/releases/*}
  base_url="$repo_root/releases/download/v$version"
fi
target="$platform-$arch"
asset=$(awk -F '\t' -v key="$target" '$1 == key {print $2;exit}' "$stage/manifest.tsv")
expected=$(awk -F '\t' -v key="$target" '$1 == key {print $3;exit}' "$stage/manifest.tsv")
size=$(awk -F '\t' -v key="$target" '$1 == key {print $4;exit}' "$stage/manifest.tsv")
case "$asset" in releases/*.tar.gz|oxgen_*.tar.gz) ;; *) printf '缺少对应系统程序\n' >&2; exit 1 ;; esac
case "$asset" in *'..'*|*[!a-zA-Z0-9_./-]*) printf '清单下载路径无效\n' >&2; exit 1 ;; esac
if ! printf '%s\n' "$expected" | awk 'length($0)==64 && $0 !~ /[^0-9a-f]/ {valid=1} END {exit !valid}'; then printf '缺少有效 SHA256\n' >&2; exit 1; fi
case "$size" in ''|*[!0-9]*) printf '无效安装包大小\n' >&2; exit 1 ;; esac
fetch "$base_url/$asset" "$stage/release.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$stage/release.tar.gz" | awk '{print $1}');
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$stage/release.tar.gz" | awk '{print $1}');
else printf '缺少系统 SHA256 工具，未安装\n' >&2; exit 1; fi
[ "$actual" = "$expected" ] || { printf 'SHA256 校验失败，未安装\n' >&2; exit 1; }
actual_size=$(wc -c <"$stage/release.tar.gz" | tr -d ' ')
[ "$actual_size" = "$size" ] || { printf '安装包大小校验失败\n' >&2; exit 1; }
tar -tzf "$stage/release.tar.gz" >"$stage/entries"
if ! awk '/^\// || /(^|\/)\.\.(\/|$)/ || /\\/ {bad=1} END{exit bad}' "$stage/entries"; then printf '安装包路径不安全\n' >&2; exit 1; fi
if ! tar -tvzf "$stage/release.tar.gz" | awk 'substr($0,1,1)=="l" || substr($0,1,1)=="h" {bad=1} END{exit bad}'; then printf '安装包不应包含符号链接\n' >&2; exit 1; fi
mkdir "$stage/unpacked"
tar -xzf "$stage/release.tar.gz" -C "$stage/unpacked"
[ -f "$stage/unpacked/bin/oxgen" ] || { printf '安装包缺少 oxgen\n' >&2; exit 1; }
mkdir -p "$install_dir/bin"
cp "$stage/unpacked/bin/oxgen" "$install_dir/bin/oxgen.new"
chmod 755 "$install_dir/bin/oxgen.new"
"$install_dir/bin/oxgen.new" --version
mv -f "$install_dir/bin/oxgen.new" "$install_dir/bin/oxgen"
printf '%s/\n' "$base_url" >"$install_dir/server-url.txt"
if [ "$no_path" = 0 ]; then
  ask '输入 1 将 oxgen 加入当前用户 PATH，其他输入跳过：'
  if [ "$answer" = 1 ]; then
    escaped=$(printf '%s' "$install_dir/bin" | sed 's/[\\$`"]/\\&/g')
    case "${SHELL:-/bin/sh}" in */zsh) profiles="$HOME/.zprofile:$HOME/.zshrc" ;; */bash) profiles="$HOME/.profile:$HOME/.bashrc" ;; *) profiles="$HOME/.profile" ;; esac
    old_ifs=$IFS; IFS=:
    for profile in $profiles; do
      line="export PATH=\"$escaped:\$PATH\""
      if [ ! -f "$profile" ] || ! grep -Fqx "$line" "$profile"; then printf '\n# OXGEN command path\n%s\n' "$line" >>"$profile"; fi
    done
    IFS=$old_ifs
    printf '已添加用户 shell 配置；请开启新终端。\n'
  fi
fi
printf '安装完成：%s/bin/oxgen\n' "$install_dir"
if [ "$no_launch" = 0 ]; then "$install_dir/bin/oxgen" --home "$install_dir" </dev/tty; fi
