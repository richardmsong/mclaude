import Foundation

enum SkillsScanner {
    /// Built-in Claude Code slash commands
    static let builtinCommands: [SkillInfo] = [
        SkillInfo(name: "clear", description: "Clear conversation history", source: "builtin"),
        SkillInfo(name: "compact", description: "Compact conversation to save context", source: "builtin"),
        SkillInfo(name: "config", description: "Open or view Claude Code configuration", source: "builtin"),
        SkillInfo(name: "cost", description: "Show token usage and cost for this session", source: "builtin"),
        SkillInfo(name: "doctor", description: "Check Claude Code installation health", source: "builtin"),
        SkillInfo(name: "help", description: "Show available commands and usage", source: "builtin"),
        SkillInfo(name: "init", description: "Initialize CLAUDE.md in current project", source: "builtin"),
        SkillInfo(name: "login", description: "Log in to your Anthropic account", source: "builtin"),
        SkillInfo(name: "logout", description: "Log out of your Anthropic account", source: "builtin"),
        SkillInfo(name: "memory", description: "View or edit CLAUDE.md memory files", source: "builtin"),
        SkillInfo(name: "model", description: "Switch the active Claude model", source: "builtin"),
        SkillInfo(name: "permissions", description: "View or modify tool permissions", source: "builtin"),
        SkillInfo(name: "review", description: "Review a pull request", source: "builtin"),
        SkillInfo(name: "status", description: "Show current session status", source: "builtin"),
        SkillInfo(name: "vim", description: "Toggle vim keybindings", source: "builtin"),
        SkillInfo(name: "terminal-setup", description: "Set up terminal integration", source: "builtin"),
        SkillInfo(name: "fast", description: "Toggle fast output mode", source: "builtin"),
        SkillInfo(name: "commit", description: "Create a git commit with staged changes", source: "builtin"),
    ]

    /// Scan all skills: builtins + global + per-project for active sessions
    static func scanAll(sessionCwds: [String]) -> [SkillInfo] {
        var skills = builtinCommands

        // Global skills from ~/.claude/skills/
        let globalDir = "\(NSHomeDirectory())/.claude/skills"
        skills.append(contentsOf: scanDirectory(globalDir, source: "global"))

        // Per-project skills from each session's cwd
        var seen = Set<String>()
        for cwd in sessionCwds {
            guard !seen.contains(cwd) else { continue }
            seen.insert(cwd)
            let projectSkillsDir = "\(cwd)/.claude/skills"
            let projectName = (cwd as NSString).lastPathComponent
            skills.append(contentsOf: scanDirectory(projectSkillsDir, source: projectName))
        }

        return skills
    }

    /// Scan a skills directory for SKILL.md files, parse frontmatter
    private static func scanDirectory(_ dir: String, source: String) -> [SkillInfo] {
        let fm = FileManager.default
        guard let entries = try? fm.contentsOfDirectory(atPath: dir) else { return [] }

        return entries.compactMap { entry in
            let skillPath = "\(dir)/\(entry)/SKILL.md"
            guard fm.fileExists(atPath: skillPath),
                  let content = try? String(contentsOfFile: skillPath, encoding: .utf8) else { return nil }
            let (name, description) = parseFrontmatter(content, fallbackName: entry)
            return SkillInfo(name: name, description: description, source: source)
        }
    }

    /// Parse YAML frontmatter from SKILL.md to extract name and description
    private static func parseFrontmatter(_ content: String, fallbackName: String) -> (String, String) {
        guard content.hasPrefix("---") else { return (fallbackName, "") }
        let lines = content.components(separatedBy: "\n")
        var name = fallbackName
        var description = ""

        for line in lines.dropFirst() {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed == "---" { break }
            if trimmed.hasPrefix("name:") {
                name = trimmed.dropFirst(5).trimmingCharacters(in: .whitespacesAndNewlines)
            } else if trimmed.hasPrefix("description:") {
                description = trimmed.dropFirst(12).trimmingCharacters(in: .whitespacesAndNewlines)
            }
        }
        return (name, description)
    }
}
