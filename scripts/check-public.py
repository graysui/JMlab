"""Inspect the tracked public tree without opening local deployment data."""
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
ROOT_FILES = {
    ".dockerignore", ".env.example", ".gitattributes", ".gitignore",
    "AGENTS.md", "CHANGELOG.md", "Dockerfile", "README.md", "go.mod", "go.sum",
}
PUBLIC_DIRS = {".github", "cmd", "deploy", "docs", "e2e", "internal", "migrations", "proto", "scripts", "web"}
FORBIDDEN_DOC = re.compile(r"javdb|javlab|播放源|成人", re.IGNORECASE)


def check(root=ROOT):
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode("utf-8").split("\0")
    errors = []
    documents = 0
    for name in filter(None, names):
        path = root / name
        if not path.exists():
            continue  # A staged deletion is not published.
        rel = PurePosixPath(name)
        if (len(rel.parts) == 1 and name not in ROOT_FILES) or (len(rel.parts) > 1 and rel.parts[0] not in PUBLIC_DIRS):
            errors.append(f"不在公开范围：{name}")
            continue
        if rel.name == ".env" or (rel.name.startswith(".env.") and rel.name != ".env.example") or rel.suffix in {".db", ".exe", ".log", ".pyc"} or any(part in {"node_modules", "dist", "__pycache__", "test-results", "playwright-report"} for part in rel.parts):
            errors.append(f"包含本地文件：{name}")
            continue
        if path.suffix in {".md", ".yaml", ".yml"}:
            documents += 1
            if FORBIDDEN_DOC.search(path.read_text(encoding="utf-8")):
                errors.append(f"公开说明含已移除的业务或旧品牌描述：{name}")
        if path.suffix == ".go" and re.search(r'"github.com/[^"\n]+/modules(?:/|")', path.read_text(encoding="utf-8")):
            errors.append(f"主程序依赖具体扩展：{name}")
    docker = (root / "Dockerfile").read_text(encoding="utf-8")
    if re.search(r"(?im)^COPY\s+.*(?:/modules|\bmodules\s)", docker):
        errors.append("Dockerfile 不应复制具体扩展")
    if errors:
        raise ValueError("\n".join(errors))
    print(f"通过：公开文件范围、{documents} 份说明、模块编译与镜像边界。")


if __name__ == "__main__":
    try:
        check()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
