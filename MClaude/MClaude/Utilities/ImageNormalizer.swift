import UIKit

/// Prepares a picked photo for upload: decodes any format UIImage understands (HEIC, PNG, JPEG…),
/// downscales so the long edge is at most `maxLongEdge` px, and re-encodes as JPEG.
/// The Claude API rejects HEIC, and the server drops images over ~3.7MB.
enum ImageNormalizer {
    static let maxLongEdge: CGFloat = 2000
    static let maxBytes = 3_700_000

    static func uploadJPEG(from data: Data) -> Data? {
        guard let image = UIImage(data: data) else { return nil }
        let size = image.size  // in points, already orientation-corrected
        let longEdge = max(size.width, size.height) * image.scale
        guard longEdge > 0 else { return nil }
        let factor = min(1, maxLongEdge / longEdge)
        let target = CGSize(width: (size.width * image.scale * factor).rounded(),
                            height: (size.height * image.scale * factor).rounded())

        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let resized = UIGraphicsImageRenderer(size: target, format: format).image { _ in
            image.draw(in: CGRect(origin: .zero, size: target))  // applies EXIF orientation
        }

        for quality in [0.8, 0.65, 0.5] as [CGFloat] {
            if let jpeg = resized.jpegData(compressionQuality: quality), jpeg.count <= maxBytes {
                return jpeg
            }
        }
        return nil
    }
}
