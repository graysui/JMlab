"""Inspect the tracked public tree without opening local deployment data."""
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
ROOT_FILES = {
    ".dockerignore", ".env.example", ".gitattributes", ".gitignore",
    "Dockerfile", "README.md", "go.mod", "go.sum",
}
PUBLIC_DIRS = {".github", "cmd", "deploy", "docs", "e2e", "internal", "migrations", "proto", "scripts", "web"}
# Only this reusable development skill is public; concrete modules stay private.
PUBLIC_SKILL_FILES = {
    "skills/jmlab-module-development/SKILL.md",
    "skills/jmlab-module-development/agents/openai.yaml",
    "skills/jmlab-module-development/references/manifest-and-metadata.md",
    "skills/jmlab-module-development/references/playback-and-host.md",
    "skills/jmlab-module-development/references/verification.md",
}
FORBIDDEN_DOC = re.compile(r"javdb|javlab|播放源|成人", re.IGNORECASE)
DOC_EXTENSIONS = {".md", ".mdx", ".rst", ".adoc", ".pdf", ".docx", ".odt"}


def readme_documents(root):
    allowed = {"README.md"}
    readme = (root / "README.md").read_text(encoding="utf-8")
    for match in re.finditer(r"\]\(([^)]+)\)", readme):
        target = match.group(1).strip()
        if target.startswith("<"):
            target = target[1:target.index(">")]
        else:
            target = target.split()[0]
        parsed = urlsplit(target)
        if parsed.scheme or parsed.netloc or not parsed.path:
            continue
        path = (root / unquote(parsed.path)).resolve()
        try:
            relative = path.relative_to(root.resolve()).as_posix()
        except ValueError:
            raise ValueError(f"README 文档链接超出项目目录：{target}") from None
        if relative.startswith("docs/") or path.suffix.lower() in DOC_EXTENSIONS:
            allowed.add(relative)
    return allowed


def check(root=ROOT):
    public_documents = readme_documents(root)
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode("utf-8").split("\0")
    errors = []
    documents = 0
    for name in filter(None, names):
        path = root / name
        if not path.exists():
            continue  # A staged deletion is not published.
        rel = PurePosixPath(name)
        if rel.suffix.lower() in DOC_EXTENSIONS and name not in public_documents:
            errors.append(f"项目文档未被 README 直接引用：{name}")
            continue
        if (len(rel.parts) == 1 and name not in ROOT_FILES) or (len(rel.parts) > 1 and rel.parts[0] not in PUBLIC_DIRS and name not in PUBLIC_SKILL_FILES):
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
    if re.search(r"(?im)^COPY\s+.*(?:/(?:local-)?modules|\b(?:local-)?modules\s)", docker):
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
