import SwiftUI

/// Parses ANSI escape sequences (SGR codes) into a SwiftUI AttributedString.
enum ANSIParser {

    static func parse(_ input: String) -> AttributedString {
        var result = AttributedString()
        var currentFG: Color = .white
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
                            currentFG = .white; currentBG = nil
                            bold = false; dim = false; italic = false
                            underline = false; strikethrough = false
                        case 1: bold = true
                        case 2: dim = true
                        case 3: italic = true
                        case 4: underline = true
                        case 7: // Reverse video — swap fg/bg
                            let oldFG = currentFG
                            currentFG = currentBG ?? .black
                            currentBG = oldFG
                        case 9: strikethrough = true
                        case 22: bold = false; dim = false
                        case 23: italic = false
                        case 24: underline = false
                        case 27: break // reverse off (would need to track)
                        case 29: strikethrough = false
                        // Standard foreground colors
                        case 30: currentFG = .black
                        case 31: currentFG = Color(.sRGB, red: 0.9, green: 0.3, blue: 0.3)
                        case 32: currentFG = Color(.sRGB, red: 0.3, green: 0.85, blue: 0.3)
                        case 33: currentFG = Color(.sRGB, red: 0.9, green: 0.85, blue: 0.3)
                        case 34: currentFG = Color(.sRGB, red: 0.4, green: 0.5, blue: 1.0)
                        case 35: currentFG = Color(.sRGB, red: 0.85, green: 0.4, blue: 0.85)
                        case 36: currentFG = Color(.sRGB, red: 0.3, green: 0.85, blue: 0.85)
                        case 37: currentFG = .white
                        case 39: currentFG = .white
                        // Standard background colors
                        case 40: currentBG = .black
                        case 41: currentBG = Color(.sRGB, red: 0.7, green: 0.2, blue: 0.2)
                        case 42: currentBG = Color(.sRGB, red: 0.2, green: 0.6, blue: 0.2)
                        case 43: currentBG = Color(.sRGB, red: 0.7, green: 0.65, blue: 0.2)
                        case 44: currentBG = Color(.sRGB, red: 0.2, green: 0.3, blue: 0.7)
                        case 45: currentBG = Color(.sRGB, red: 0.65, green: 0.2, blue: 0.65)
                        case 46: currentBG = Color(.sRGB, red: 0.2, green: 0.65, blue: 0.65)
                        case 47: currentBG = Color(.sRGB, red: 0.75, green: 0.75, blue: 0.75)
                        case 49: currentBG = nil
                        // Bright foreground colors
                        case 90: currentFG = .gray
                        case 91: currentFG = Color(.sRGB, red: 1.0, green: 0.4, blue: 0.4)
                        case 92: currentFG = Color(.sRGB, red: 0.4, green: 1.0, blue: 0.4)
                        case 93: currentFG = Color(.sRGB, red: 1.0, green: 1.0, blue: 0.4)
                        case 94: currentFG = Color(.sRGB, red: 0.5, green: 0.6, blue: 1.0)
                        case 95: currentFG = Color(.sRGB, red: 1.0, green: 0.5, blue: 1.0)
                        case 96: currentFG = Color(.sRGB, red: 0.4, green: 1.0, blue: 1.0)
                        case 97: currentFG = Color(.sRGB, red: 1.0, green: 1.0, blue: 1.0)
                        // Bright background colors
                        case 100: currentBG = Color(.sRGB, red: 0.5, green: 0.5, blue: 0.5)
                        case 101: currentBG = Color(.sRGB, red: 1.0, green: 0.4, blue: 0.4)
                        case 102: currentBG = Color(.sRGB, red: 0.4, green: 1.0, blue: 0.4)
                        case 103: currentBG = Color(.sRGB, red: 1.0, green: 1.0, blue: 0.4)
                        case 104: currentBG = Color(.sRGB, red: 0.5, green: 0.6, blue: 1.0)
                        case 105: currentBG = Color(.sRGB, red: 1.0, green: 0.5, blue: 1.0)
                        case 106: currentBG = Color(.sRGB, red: 0.4, green: 1.0, blue: 1.0)
                        case 107: currentBG = Color(.sRGB, red: 1.0, green: 1.0, blue: 1.0)
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
                                    currentBG = color256(codes[ci + 2])
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
        guard (0...255).contains(n) else { return .white }

        if n < 16 {
            let table: [(Double, Double, Double)] = [
                (0, 0, 0), (0.7, 0.2, 0.2), (0.2, 0.7, 0.2), (0.7, 0.65, 0.2),
                (0.2, 0.3, 0.8), (0.7, 0.2, 0.7), (0.2, 0.7, 0.7), (0.75, 0.75, 0.75),
                (0.5, 0.5, 0.5), (1, 0.4, 0.4), (0.4, 1, 0.4), (1, 1, 0.4),
                (0.5, 0.6, 1), (1, 0.5, 1), (0.4, 1, 1), (1, 1, 1),
            ]
            let (r, g, b) = table[n]
            return Color(.sRGB, red: r, green: g, blue: b)
        }

        if n < 232 {
            let idx = n - 16
            let ri = idx / 36
            let gi = (idx % 36) / 6
            let bi = idx % 6
            let vals: [Double] = [0, 0.37, 0.53, 0.69, 0.84, 1.0]
            return Color(.sRGB, red: vals[ri], green: vals[gi], blue: vals[bi])
        }

        let gray = Double(n - 232) / 23.0
        return Color(.sRGB, red: gray, green: gray, blue: gray)
    }
}
