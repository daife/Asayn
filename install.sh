#!/bin/bash
set -e

# Asayn 安装脚本
echo "=== Asayn 安装脚本 ==="
echo ""



migrate_claude_code_assets() {
    echo ""
    read -p "是否迁移 Claude Code 的 skills 和 MCP 配置？(y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        return 0
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo "未找到 python3，无法自动解析 Claude Code 配置；已跳过迁移。"
        return 0
    fi
    python3 <<'PYCODE'
import json
import os
import re
import shutil
from pathlib import Path

home = Path.home()
cwd = Path.cwd()
asayn_dir = home / ".Asayn"
asayn_skills = asayn_dir / "skills"
asayn_mcp = asayn_dir / "mcp"
asayn_skills.mkdir(parents=True, exist_ok=True)
asayn_mcp.mkdir(parents=True, exist_ok=True)

def uniq_paths(paths):
    seen = set()
    out = []
    for p in paths:
        try:
            rp = p.expanduser().resolve()
        except Exception:
            rp = p.expanduser()
        key = str(rp)
        if key not in seen and rp.exists():
            seen.add(key)
            out.append(rp)
    return out

def parse_skill_name(skill_file):
    try:
        text = skill_file.read_text(encoding="utf-8", errors="ignore")
    except Exception:
        return ""
    if text.startswith("---\n"):
        rest = text[4:]
        end = rest.find("\n---")
        if end >= 0:
            front = rest[:end]
            for line in front.splitlines():
                if ":" not in line:
                    continue
                k, v = line.split(":", 1)
                if k.strip() == "name":
                    return v.strip().strip("'\"")
    return ""

def existing_asayn_skill_names():
    names = set()
    if not asayn_skills.exists():
        return names
    for child in asayn_skills.iterdir():
        if not child.is_dir():
            continue
        sf = child / "SKILL.md"
        if sf.exists():
            names.add(parse_skill_name(sf) or child.name)
            names.add(child.name)
    return names

def discover_skills():
    roots = []
    cfg = os.environ.get("CLAUDE_CONFIG_DIR", "").strip()
    if cfg:
        roots.append(Path(cfg) / "skills")
        roots.append(Path(cfg) / "plugins")
    roots += [
        home / ".claude" / "skills",
        home / ".claude" / "plugins",
        home / ".config" / "claude-code" / "skills",
        home / ".config" / "claude-code" / "plugins",
        cwd / ".claude" / "skills",
        cwd / ".claude" / "plugins",
    ]
    roots = uniq_paths(roots)
    existing = existing_asayn_skill_names()
    found = []
    seen = set()
    for root in roots:
        if root.is_file():
            continue
        for sf in root.rglob("SKILL.md"):
            folder = sf.parent
            key = str(folder.resolve())
            if key in seen:
                continue
            seen.add(key)
            name = parse_skill_name(sf) or folder.name
            target = asayn_skills / folder.name
            duplicate = target.exists() or name in existing or folder.name in existing
            found.append({
                "kind": "skill",
                "name": name,
                "folder_name": folder.name,
                "source": str(folder),
                "target": str(target),
                "duplicate": duplicate,
                "reason": "已存在同名 skill 或目标目录" if duplicate else "",
            })
    found.sort(key=lambda x: (x["name"].lower(), x["source"]))
    return found

def collect_mcp_servers(obj, source, out):
    if isinstance(obj, dict):
        servers = obj.get("mcpServers")
        if isinstance(servers, dict):
            for name, cfg in servers.items():
                if isinstance(cfg, dict):
                    out.append((str(name), cfg, source))
        for v in obj.values():
            collect_mcp_servers(v, source, out)
    elif isinstance(obj, list):
        for v in obj:
            collect_mcp_servers(v, source, out)

def existing_asayn_mcp_names():
    names = set()
    roots = [asayn_mcp, cwd / ".Asayn" / "mcp"]
    for root in roots:
        if not root.exists():
            continue
        for jf in root.glob("*.json"):
            try:
                data = json.loads(jf.read_text(encoding="utf-8"))
            except Exception:
                continue
            servers = data.get("mcpServers") if isinstance(data, dict) else None
            if isinstance(servers, dict):
                names.update(str(k) for k in servers.keys())
    return names

def json_files_under(root):
    if root.is_file():
        return [root] if root.suffix.lower() == ".json" else []
    files = []
    for jf in root.rglob("*.json"):
        try:
            if jf.stat().st_size > 5 * 1024 * 1024:
                continue
        except Exception:
            continue
        files.append(jf)
    return files

def discover_mcp():
    roots = []
    cfg = os.environ.get("CLAUDE_CONFIG_DIR", "").strip()
    if cfg:
        roots.append(Path(cfg))
    roots += [
        home / ".claude.json",
        home / ".claude",
        home / ".config" / "claude",
        home / ".config" / "claude-code",
        cwd / ".mcp.json",
        cwd / ".claude",
    ]
    files = []
    seen_files = set()
    for root in uniq_paths(roots):
        for jf in json_files_under(root):
            key = str(jf.resolve())
            if key not in seen_files:
                seen_files.add(key)
                files.append(jf)
    raw = []
    for jf in files:
        try:
            data = json.loads(jf.read_text(encoding="utf-8"))
        except Exception:
            continue
        collect_mcp_servers(data, str(jf), raw)
    existing = existing_asayn_mcp_names()
    by_name = {}
    for name, cfg, source in raw:
        if name in by_name:
            continue
        duplicate = name in existing or (asayn_mcp / (safe_filename(name) + ".json")).exists()
        by_name[name] = {
            "kind": "mcp",
            "name": name,
            "config": cfg,
            "source": source,
            "duplicate": duplicate,
            "reason": "已存在同名 MCP server 或配置文件" if duplicate else "",
        }
    return [by_name[k] for k in sorted(by_name, key=str.lower)]

def safe_filename(name):
    safe = re.sub(r"[^A-Za-z0-9._-]+", "_", name).strip("._-")
    return safe or "mcp_server"

def parse_selection(raw, default_ids, max_id, blocked):
    raw = raw.strip().lower()
    if raw == "":
        return set(default_ids)
    if raw in {"a", "all"}:
        return {i for i in range(1, max_id + 1) if i not in blocked}
    if raw in {"n", "none", "no"}:
        return set()
    selected = set()
    for part in re.split(r"[,\s]+", raw):
        if not part:
            continue
        if "-" in part:
            a, b = part.split("-", 1)
            if a.isdigit() and b.isdigit():
                lo, hi = sorted((int(a), int(b)))
                selected.update(range(lo, hi + 1))
        elif part.isdigit():
            selected.add(int(part))
    return {i for i in selected if 1 <= i <= max_id and i not in blocked}

items = discover_skills() + discover_mcp()
if not items:
    print("未发现可迁移的 Claude Code skills 或 MCP 配置。")
    raise SystemExit(0)

print("\n发现以下 Claude Code 可迁移项：")
indexed = {}
default_ids = []
blocked = set()
idx = 1
for item in items:
    status = "已存在，跳过" if item["duplicate"] else "默认迁移"
    typ = "Skill" if item["kind"] == "skill" else "MCP"
    print(f"  [{idx}] {typ}: {item['name']}  ({status})")
    print(f"      来源: {item['source']}")
    if item["duplicate"]:
        blocked.add(idx)
    else:
        default_ids.append(idx)
    indexed[idx] = item
    idx += 1

print("\n输入要迁移的编号，例如 1,3,5 或 2-4；a=全部可迁移；n=不迁移；直接回车=迁移全部非重复项。")
selected = parse_selection(input("选择: "), default_ids, len(indexed), blocked)
if not selected:
    print("未选择任何迁移项。")
    raise SystemExit(0)

print("\n将迁移：")
for i in sorted(selected):
    item = indexed[i]
    print(f"  - {'Skill' if item['kind']=='skill' else 'MCP'}: {item['name']}")
confirm = input("确认迁移？(y/N): ").strip().lower()
if confirm not in {"y", "yes"}:
    print("已取消迁移。")
    raise SystemExit(0)

migrated = []
skipped = []
for i in sorted(selected):
    item = indexed[i]
    try:
        if item["kind"] == "skill":
            target = Path(item["target"])
            if target.exists():
                skipped.append(f"skill {item['name']}：目标目录已存在")
                continue
            shutil.copytree(item["source"], target)
            migrated.append(f"skill {item['name']} -> {target}")
        else:
            name = item["name"]
            if name in existing_asayn_mcp_names():
                skipped.append(f"mcp {name}：同名配置已存在")
                continue
            base = safe_filename(name)
            target = asayn_mcp / f"{base}.json"
            suffix = 1
            while target.exists():
                target = asayn_mcp / f"{base}_{suffix}.json"
                suffix += 1
            target.write_text(json.dumps({"mcpServers": {name: item["config"]}}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            migrated.append(f"mcp {name} -> {target}")
    except Exception as exc:
        skipped.append(f"{item['kind']} {item['name']}：{exc}")

print("\n迁移完成。")
if migrated:
    print("已迁移：")
    for line in migrated:
        print("  - " + line)
if skipped:
    print("已跳过：")
    for line in skipped:
        print("  - " + line)
PYCODE
}

# 检查 ~/.Asayn 文件夹是否存在
if [ -d "$HOME/.Asayn" ]; then
    echo "检测到已存在的 ~/.Asayn 文件夹。"
    read -p "是否清空并重新安装？(y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "正在清空 ~/.Asayn 文件夹..."
        rm -rf "$HOME/.Asayn"
        echo "清空完成。"
    else
        echo "保留现有文件夹。"
    fi
fi

# 获取最新版本
echo "正在获取最新版本信息..."
LATEST_VERSION=$(curl -s https://api.github.com/repos/daife/Asayn/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_VERSION" ]; then
    echo "错误：无法获取最新版本信息"
    exit 1
fi

echo "最新版本: $LATEST_VERSION"

# 检测系统架构
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        echo "不支持的架构: $ARCH"
        exit 1
        ;;
esac

echo "系统架构: $ARCH"

# 创建安装目录
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"

# 下载二进制文件
echo "正在下载 Asayn $LATEST_VERSION..."
DOWNLOAD_URL="https://github.com/daife/Asayn/releases/download/$LATEST_VERSION/asayn-linux-$ARCH"
curl -L -o "$INSTALL_DIR/asayn" "$DOWNLOAD_URL"

# 设置可执行权限
chmod +x "$INSTALL_DIR/asayn"

# 检查并更新 PATH（当前会话和永久性）
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "正在添加 $INSTALL_DIR 到 PATH..."
    
    # 更新当前会话的 PATH
    export PATH="$INSTALL_DIR:$PATH"
    
    # 检查 shell 配置文件并添加 PATH
    if [ -f "$HOME/.bashrc" ]; then
        # 检查是否已经添加过
        if ! grep -q "export PATH=\"\$HOME/.local/bin:\$PATH\"" "$HOME/.bashrc"; then
            echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "$HOME/.bashrc"
            echo "已添加到 ~/.bashrc"
        fi
    fi
    
    if [ -f "$HOME/.zshrc" ]; then
        # 检查是否已经添加过
        if ! grep -q "export PATH=\"\$HOME/.local/bin:\$PATH\"" "$HOME/.zshrc"; then
            echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "$HOME/.zshrc"
            echo "已添加到 ~/.zshrc"
        fi
    fi
    
    # 刷新环境变量
    source "$HOME/.bashrc" 2>/dev/null || true
    source "$HOME/.zshrc" 2>/dev/null || true
    
    echo "PATH 已更新，当前终端已生效。" -e "\033[32m"
fi

echo ""
echo "=== 安装完成 ==="
echo "Asayn 已安装到: $INSTALL_DIR/asayn"
echo ""
echo "环境变量已自动配置，无需重启终端。" -e "\033[32m"
echo ""
echo "配置文件位置:"
echo "  ~/.Asayn/api_config.toml"
echo "  在此文件中配置您的 API 密钥(首次运行后才会自动生成该目录)"
echo ""
migrate_claude_code_assets

echo "使用方法:"
echo "  cd /path/to/your/project"
echo "  asayn"
