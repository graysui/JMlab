#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "用法: $0 <版本号，如 v1.2.3> [输出目录]" >&2
  exit 2
fi

version="$1"
if [[ ! "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号必须符合 SemVer，例如 v1.2.3 或 v1.2.3-rc.1" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
if git -C "$repo_root" rev-parse HEAD >/dev/null 2>&1; then
  commit="$(git -C "$repo_root" rev-parse HEAD)"
  source_mode="git"
else
  commit="${JMLAB_REVISION:-unknown}"
  source_mode="directory"
fi
image="jmlab:$version"
platform="linux/amd64"
release_name="jmlab-${version}-linux-amd64"
output_dir="${2:-$repo_root/dist/releases/$release_name}"
if [[ "$output_dir" != /* ]]; then
  output_dir="$repo_root/$output_dir"
fi

if [[ -e "$output_dir" ]] && [[ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  echo "输出目录已经存在且不为空: $output_dir" >&2
  exit 1
fi

command -v docker >/dev/null || { echo "需要 Docker CLI" >&2; exit 1; }
command -v jq >/dev/null || { echo "需要 jq" >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  echo "需要 sha256sum 或 shasum" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT INT TERM

archive="$tmp_dir/source.tar"
docker_archive="$tmp_dir/$release_name.tar"
if [[ "$source_mode" == "git" ]]; then
  git -C "$repo_root" archive --format=tar --output="$archive" "$commit"
else
  tar -cf "$archive" -C "$repo_root" .
fi
source_dir="$tmp_dir/source"
mkdir -p "$source_dir"
tar -xf "$archive" -C "$source_dir"
echo "构建 $image ($platform)，源码提交 $commit"
docker build --platform "$platform" \
  --build-arg "JMLAB_VERSION=$version" \
  --build-arg "JMLAB_REVISION=$commit" \
  --tag "$image" "$source_dir"
docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}' | grep -Fx "$platform" >/dev/null
docker image inspect "$image" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' | grep -Fx "$version" >/dev/null
docker save --output "$docker_archive" "$image"

mkdir -p "$output_dir/docs"
cp "$docker_archive" "$output_dir/$release_name.tar"
cp "$repo_root/deploy/docker-compose.release.yml" "$output_dir/docker-compose.yml"
cp "$repo_root/deploy/release.env.example" "$output_dir/.env.example"
cp "$repo_root/README.md" "$output_dir/README.md"
cp "$repo_root/CHANGELOG.md" "$output_dir/CHANGELOG.md"
cp "$repo_root/docs/release-install.md" "$output_dir/docs/release-install.md"
cp "$repo_root/docs/release-checklist.md" "$output_dir/docs/release-checklist.md"
cp "$repo_root/docs/jellyfin-compatibility.md" "$output_dir/docs/jellyfin-compatibility.md"
cp "$repo_root/docs/openapi.yaml" "$output_dir/docs/openapi.yaml"

created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n \
  --arg product "JMlab" \
  --arg version "$version" \
  --arg image "$image" \
  --arg platform "$platform" \
  --arg commit "$commit" \
  --arg createdAt "$created_at" \
  --arg imageArchive "$release_name.tar" \
  --arg imageSha256 "$(sha256 "$output_dir/$release_name.tar")" \
  --arg composeSha256 "$(sha256 "$output_dir/docker-compose.yml")" \
  '{product:$product,version:$version,image:$image,platform:$platform,gitCommit:$commit,createdAt:$createdAt,files:[{path:$imageArchive,sha256:$imageSha256},{path:"docker-compose.yml",sha256:$composeSha256}]}' \
  >"$output_dir/manifest.json"
(cd "$output_dir" && sha256sum .env.example README.md CHANGELOG.md docker-compose.yml manifest.json "$release_name.tar" docs/* >SHA256SUMS 2>/dev/null) || {
  (cd "$output_dir" && shasum -a 256 .env.example README.md CHANGELOG.md docker-compose.yml manifest.json "$release_name.tar" docs/* >SHA256SUMS)
}

JMLAB_IMAGE="$image" docker compose --env-file "$output_dir/.env.example" \
  -f "$output_dir/docker-compose.yml" config --quiet
if command -v sha256sum >/dev/null; then
  (cd "$output_dir" && sha256sum --check SHA256SUMS)
else
  (cd "$output_dir" && shasum -a 256 --check SHA256SUMS)
fi

echo "发布包完成: $output_dir"
echo "镜像: $image ($platform)"
