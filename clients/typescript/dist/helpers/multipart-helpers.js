export function createFilePartDescriptor(partName, fileInput, defaultContentType) {
    if (fileInput.contents) {
        return {
            name: partName,
            body: fileInput.contents,
            contentType: fileInput.contentType ?? defaultContentType,
            filename: fileInput.filename,
        };
    }
    else {
        return {
            name: partName,
            body: fileInput,
            contentType: defaultContentType,
        };
    }
}
//# sourceMappingURL=multipart-helpers.js.map