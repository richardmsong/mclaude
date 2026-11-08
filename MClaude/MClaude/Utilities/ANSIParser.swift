import SwiftUI

/// Parses ANSI escape sequences (SGR codes) into a SwiftUI AttributedString.
enum ANSIParser {

    // MARK: - Warm palette (reads on both ivory and warm-charcoal surfaces)

    /// Default foreground (SGR 39 / reset).
    static let defaultFG = Theme.textPrimary
    /// Terminal surface; also the "foreground" used for reverse video with no explicit bg.
    static let surface = Theme.bgSunken

    /// The 16 base colours (0-7 normal, 8-15 bright), each light/dark adaptive and warm-leaning.
    private static let base16: [Color] = [
        Color(light: 0x3D3D3A, dark: 0x8A8880),  // 0 black (warm charcoal / warm grey)
        Color(light: 0xB53333, dark: 0xE06C6C),  // 1 red
        Color(light: 0x3F7F1F, dark: 0x8FC46A),  // 2 green
        Color(light: 0x99690F, dark: 0xD9A441),  // 3 yellow (ochre)
        Color(light: 0x2C6AAE, dark: 0x7AACE0),  // 4 blue (muted)
        Color(light: 0x94487E, dark: 0xCB8DBD),  // 5 magenta (plum)
        Color(light: 0x2A7F78, dark: 0x72BFB4),  // 6 cyan (teal)
        Color(light: 0x5E5D58, dark: 0xC2C0B6),  // 7 white (warm light grey)
        Color(light: 0x73726C, dark: 0x9C9A92),  // 8 bright black
        Color(light: 0xC6613F, dark: 0xF0907A),  // 9 bright red (clay-leaning)
        Color(light: 0x4E9A2A, dark: 0xA8D88A),  // 10 bright green
        Color(light: 0xA8741A, dark: 0xE8C070),  // 11 bright yellow
        Color(light: 0x3A7FC4, dark: 0x9CC2EA),  // 12 bright blue
        Color(light: 0xA85A94, dark: 0xDDA8D2),  // 13 bright magenta
        Color(light: 0x34928A, dark: 0x96D4CA),  // 14 bright cyan
        Color(light: 0x141413, dark: 0xFAF9F5),  // 15 bright white (= text primary)
    ]

    /// Background variants of the base colours: translucent tints so text stays readable.
    private static let base16BG: [Color] = base16.map { $0.opacity(0.22) }

    static func parse(_ input: String) -> AttributedString {
        var result = AttributedString()
        var currentFG: Color = defaultFG
        var currentBG: Color? = nil
        var bold = false
        var dim = false
        var italic = false
        var underline = false
        var strikethrough = false

        let chars = Array(input.unicodeScalars)
        var i = chars.startIndex
        var textStart = i

        func applyStyle(_ attr: inout AttributedString) {
            attr.foregroundColor = dim ? currentFG.opacity(0.6) : currentFG
            if let bg = currentBG { attr.backgroundColor = bg }
            // Build font with combined traits
            if bold && italic {
                attr.font = .system(size: 11, weight: .bold, design: .monospaced).italic()
            } else if bold {
                attr.font = .system(size: 11, weight: .bold, design: .monospaced)
            } else if italic {
                attr.font = .system(size: 11, design: .monospaced).italic()
            }
            if underline { attr.underlineStyle = .single }
            if strikethrough { attr.strikethroughStyle = .single }
        }

        while i < chars.endIndex {
            // Look for ESC [ ... m
            if chars[i] == "\u{1B}", i + 1 < chars.endIndex, chars[i + 1] == "[" {
                // Flush text before this escape
                if textStart < i {
                    let text = String(String.UnicodeScalarView(chars[textStart..<i]))
                    var attr = AttributedString(text)
                    applyStyle(&attr)
                    result += attr
                }

                // Parse the CSI sequence
                var j = i + 2 // skip ESC [
                var paramStr = ""
                while j < chars.endIndex {
                    let c = chars[j]
                    if c >= "0" && c <= "9" || c == ";" {
                        paramStr.append(Character(c))
                        j += 1
                    } else {
                        break
                    }
                }

                // Only handle SGR (ending with 'm')
                if j < chars.endIndex, chars[j] == "m" {
                    let codes = paramStr.isEmpty ? [0] : paramStr.split(separator: ";").compactMap { Int($0) }
                    var ci = 0
                    while ci < codes.count {
                        let code = codes[ci]
                        switch code {
                        case 0: // Reset
                            currentFG = defaultFG; currentBG = nil
                            bold = false; dim = false; italic = false
                            underline = false; strikethrough = false
                        case 1: bold = true
                        case 2: dim = true
                        case 3: italic = true
                        case 4: underline = true
                        case 7: // Reverse video — swap fg/bg
                            let oldFG = currentFG
                            currentFG = currentBG ?? surface
                            currentBG = oldFG
                        case 9: strikethrough = true
                        case 22: bold = false; dim = false
                        case 23: italic = false
                        case 24: underline = false
                        case 27: break // reverse off (would need to track)
                        case 29: strikethrough = false
                        // Standard / bright foreground colors
                        case 30...37: currentFG = base16[code - 30]
                        case 39: currentFG = defaultFG
                        case 90...97: currentFG = base16[code - 90 + 8]
                        // Standard / bright background colors
                        case 40...47: currentBG = base16BG[code - 40]
                        case 49: currentBG = nil
                        case 100...107: currentBG = base16BG[code - 100 + 8]
                        // Extended colors: 38;5;N (256) or 38;2;R;G;B (truecolor)
                        case 38:
                            if ci + 1 < codes.count {
                                if codes[ci + 1] == 5, ci + 2 < codes.count {
                                    currentFG = color256(codes[ci + 2])
                                    ci += 2
                                } else if codes[ci + 1] == 2, ci + 4 < codes.count {
                                    let r = Double(codes[ci + 2]) / 255.0
                                    let g = Double(codes[ci + 3]) / 255.0
                                    let b = Double(codes[ci + 4]) / 255.0
                                    currentFG = Color(.sRGB, red: r, green: g, blue: b)
                                    ci += 4
                                }
                            }
                        case 48:
                            if ci + 1 < codes.count {
                                if codes[ci + 1] == 5, ci + 2 < codes.count {
                                    currentBG = codes[ci + 2] < 16 ? base16BG[max(0, codes[ci + 2])] : color256(codes[ci + 2])
                                    ci += 2
                                } else if codes[ci + 1] == 2, ci + 4 < codes.count {
                                    let r = Double(codes[ci + 2]) / 255.0
                                    let g = Double(codes[ci + 3]) / 255.0
                                    let b = Double(codes[ci + 4]) / 255.0
                                    currentBG = Color(.sRGB, red: r, green: g, blue: b)
                                    ci += 4
                                }
                            }
                        default: break
                        }
                        ci += 1
                    }
                    i = j + 1
                } else {
                    // Unknown CSI sequence — skip to the terminating letter
                    while j < chars.endIndex {
                        let c = chars[j]
                        if c >= "@" && c <= "~" { j += 1; break }
                        j += 1
                    }
                    i = j
                }
                textStart = i
            } else {
                i += 1
            }
        }

        // Flush remaining text
        if textStart < chars.endIndex {
            let text = String(String.UnicodeScalarView(chars[textStart..<chars.endIndex]))
            var attr = AttributedString(text)
            applyStyle(&attr)
            result += attr
        }

        return result
    }

    // MARK: - 256-color palette

    private static func color256(_ n: Int) -> Color {
        guard (0...255).contains(n) else { return defaultFG }

        if n < 16 { return base16[n] }

        if n < 232 {
            let idx = n - 16
            let ri = idx / 36
            let gi = (idx % 36) / 6
            let bi = idx % 6
            let vals: [Double] = [0, 0.37, 0.53, 0.69, 0.84, 1.0]
            return Color(.sRGB, red: vals[ri], green: vals[gi], blue: vals[bi])
        }

        // Grayscale ramp: warm-tinted and flipped in light mode so "light grey on dark"
        // output still reads on the ivory surface.
        let level = CGFloat(n - 232) / 23.0
        return Color(uiColor: UIColor { traits in
            let v = traits.userInterfaceStyle == .dark ? 0.12 + level * 0.86 : 0.88 - level * 0.80
            return UIColor(red: min(1, v + 0.01), green: v, blue: max(0, v - 0.03), alpha: 1)
        })
    }
}
