import { useState } from 'react';
import { X, FileText, Upload } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { SUPPORTED_EXTENSIONS, classifyFiles, fileIcon } from '../lib/upload-files';

const ACCEPT_STRING = SUPPORTED_EXTENSIONS.join(',');
const UPLOAD_TIMEOUT_MS = 30000;

export default function UploadModal({ collection, onClose, onSuccess }) {
  const [files, setFiles] = useState([]);
  const [collectionName, setCollectionName] = useState(collection || '');
  const [lang, setLang] = useState('en');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [dragActive, setDragActive] = useState(false);
  const [uploadProgress, setUploadProgress] = useState({ current: 0, total: 0 });
  const [successCount, setSuccessCount] = useState(0);
  const [extractImages, setExtractImages] = useState(false);

  const handleFiles = (selectedFiles) => {
    if (!selectedFiles || selectedFiles.length === 0) return;
    const { accepted, errors } = classifyFiles(selectedFiles);
    setError(errors.length > 0 ? errors.join('; ') : null);
    setFiles(accepted);
  };

  const handleDrag = (e) => {
    e.preventDefault();
    e.stopPropagation();
    // Bound to dragenter, dragover and dragleave only.
    setDragActive(e.type !== 'dragleave');
  };

  const handleDrop = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setDragActive(false);

    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      handleFiles(e.dataTransfer.files);
    }
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);
    setSuccessCount(0);
    setUploadProgress({ current: 0, total: files.length });

    const finalLang = lang.trim() || 'en';
    const finalCollection = collectionName.trim() || 'default';

    if (files.length === 0) {
      setError('No files selected.');
      setLoading(false);
      return;
    }

    const errors = [];
    let successfulUploads = 0;

    for (let i = 0; i < files.length; i++) {
      const fileData = files[i];
      setUploadProgress({ current: i + 1, total: files.length });

      try {
        const timeout = new Promise((_, reject) =>
          setTimeout(() => reject(new Error('Upload timeout (>30s)')), UPLOAD_TIMEOUT_MS)
        );
        // One door for every format: the server reads Markdown frontmatter.
        await Promise.race([
          mddbClient.uploadFile({
            files: fileData.file,
            collection: finalCollection,
            lang: finalLang,
            key: fileData.key,
            extractImages,
          }),
          timeout,
        ]);
        successfulUploads++;
        setSuccessCount(successfulUploads);
      } catch (err) {
        errors.push(`${fileData.key}: ${err.message}`);
      }
    }

    setLoading(false);

    if (errors.length > 0) {
      setError(`Uploaded ${successfulUploads}/${files.length} files. Errors: ${errors.join('; ')}`);
      if (successfulUploads > 0) {
        setTimeout(() => onSuccess(finalCollection), 2000);
      }
    } else {
      onSuccess(finalCollection);
    }
  };

  const openImport = () => {
    useStore.getState().setViewMode('import');
    onClose();
  };

  const formatModes = files.reduce((acc, f) => {
    if (f.converted) acc.converted++;
    else acc.direct++;
    return acc;
  }, { direct: 0, converted: 0 });

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-bold text-gray-900">
              {collection ? 'Upload Documents' : 'Create Collection & Upload'}
            </h2>
            <button onClick={onClose} className="text-gray-600 hover:text-gray-800" disabled={loading} aria-label="Close upload dialog">
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            {/* File Drop Zone */}
            <div
              onDragEnter={handleDrag}
              onDragLeave={handleDrag}
              onDragOver={handleDrag}
              onDrop={handleDrop}
              className={`mb-4 border-2 border-dashed rounded-lg p-8 text-center transition-colors ${
                dragActive
                  ? 'border-blue-500 bg-blue-50'
                  : 'border-gray-300 hover:border-gray-400'
              }`}
            >
              {files.length > 0 ? (
                <div>
                  <FileText className="w-12 h-12 text-blue-600 mx-auto mb-2" />
                  <p className="text-sm font-medium text-gray-900 mb-2">
                    {files.length} file{files.length > 1 ? 's' : ''} selected
                  </p>
                  <div className="max-h-40 overflow-y-auto mb-3">
                    {files.map((fileData, idx) => (
                      <div key={idx} className="text-xs text-gray-600 py-1 flex items-center justify-center gap-1">
                        <span>{fileIcon(fileData.file.name)}</span>
                        <span>{fileData.file.name}</span>
                        <span className="text-gray-600">({(fileData.file.size / 1024).toFixed(1)} KB)</span>
                        {fileData.converted && (
                          <span className="inline-flex px-1.5 py-0.5 rounded text-[10px] bg-amber-100 text-amber-700">
                            auto-convert
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                  <button
                    type="button"
                    onClick={() => {
                      setFiles([]);
                      setError(null);
                    }}
                    className="mt-2 text-sm text-blue-600 hover:text-blue-700"
                    disabled={loading}
                  >
                    Choose different files
                  </button>
                </div>
              ) : (
                <div>
                  <Upload className="w-12 h-12 text-gray-400 mx-auto mb-2" />
                  <p className="text-sm font-medium text-gray-900 mb-1">
                    Drop your files here
                  </p>
                  <p className="text-xs text-gray-500 mb-3">or</p>
                  <label className="inline-block px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 cursor-pointer transition-colors">
                    Browse Files
                    <input
                      type="file"
                      accept={ACCEPT_STRING}
                      multiple
                      onChange={(e) => handleFiles(e.target.files)}
                      className="hidden"
                    />
                  </label>
                  <p className="text-xs text-gray-500 mt-3">
                    Supported: Markdown, text, HTML, PDF, Office/OpenDocument, CSV/TSV, EPUB, e-mail, YAML, LaTeX (max 10MB each)
                  </p>
                  <p className="text-xs text-gray-600 mt-1">
                    Other formats are converted to Markdown; Markdown frontmatter becomes metadata
                  </p>
                </div>
              )}
            </div>

            {/* File summary with conversion info */}
            {files.length > 0 && (formatModes.converted > 0) && (
              <div className="mb-4 p-4 bg-gray-50 rounded-lg">
                <p className="text-sm font-medium text-gray-700 mb-2">
                  Upload Summary
                </p>
                <div className="space-y-1 text-xs text-gray-600">
                  {formatModes.direct > 0 && (
                    <div className="flex items-center gap-1">
                      <span className="w-2 h-2 rounded-full bg-green-500"></span>
                      {formatModes.direct} Markdown/text file{formatModes.direct !== 1 ? 's' : ''} (stored as written, frontmatter read by the server)
                    </div>
                  )}
                  {formatModes.converted > 0 && (
                    <div className="flex items-center gap-1">
                      <span className="w-2 h-2 rounded-full bg-amber-500"></span>
                      {formatModes.converted} file{formatModes.converted !== 1 ? 's' : ''} will be converted to markdown
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* Collection input */}
            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Collection {!collection && <span className="text-red-600">*</span>}
              </label>
              <input
                type="text"
                value={collectionName}
                onChange={(e) => setCollectionName(e.target.value)}
                placeholder="Enter collection name"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                disabled={loading || !!collection}
              />
              <p className="text-xs text-gray-500 mt-1">
                {collection
                  ? `Uploading to: ${collection}`
                  : 'All files will be uploaded to this collection'
                }
              </p>
            </div>

            {/* Language input */}
            <div className="mb-4">
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Language (applies to all files)
              </label>
              <input
                type="text"
                value={lang}
                onChange={(e) => setLang(e.target.value)}
                placeholder="en"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                disabled={loading}
              />
            </div>

            {/* Images (2.18.9): pictures inside Markdown and PDFs become image documents */}
            <div className="mb-4 flex items-start gap-2">
              <input
                id="upload-extract-images"
                type="checkbox"
                checked={extractImages}
                onChange={(e) => setExtractImages(e.target.checked)}
                className="mt-1"
                disabled={loading}
              />
              <label htmlFor="upload-extract-images" className="text-sm text-gray-700">
                Extract images
                <span className="block text-xs text-gray-600">
                  Images in PDFs and Markdown become image documents under KEY/images/ (on by default in an images collection)
                </span>
              </label>
            </div>

            {/* 2.19.0: the other ways in — JSON documents, a URL, a wiki dump */}
            <p className="mb-4 text-xs text-gray-700">
              Pasting several documents as JSON, importing a URL or a Wikipedia dump?{' '}
              <button type="button" onClick={openImport} className="underline text-blue-800" disabled={loading}>
                Open the Import page
              </button>
            </p>

            {/* Error display */}
            {error && (
              <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg">
                <p className="text-sm text-red-800">{error}</p>
              </div>
            )}

            {/* Buttons */}
            <div className="flex justify-end gap-3">
              <button
                type="button"
                onClick={onClose}
                className="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors"
                disabled={loading}
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={files.length === 0 || loading}
                className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
              >
                {loading ? (
                  <>
                    <div className="animate-spin rounded-full h-4 w-4 border-2 border-white border-t-transparent"></div>
                    Uploading {uploadProgress.current}/{uploadProgress.total}...
                  </>
                ) : (
                  <>
                    <Upload className="w-4 h-4" />
                    Upload {files.length} File{files.length !== 1 ? 's' : ''}
                  </>
                )}
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Loading overlay */}
      {loading && (
        <div className="absolute inset-0 bg-white bg-opacity-90 flex items-center justify-center rounded-lg">
          <div className="text-center">
            <div className="animate-spin rounded-full h-12 w-12 border-4 border-blue-600 border-t-transparent mx-auto mb-4"></div>
            <p className="text-sm font-medium text-gray-900">
              Uploading files...
            </p>
            <p className="text-sm text-gray-600 mt-1">
              {uploadProgress.current} of {uploadProgress.total} files
            </p>
            {successCount > 0 && (
              <p className="text-xs text-green-600 mt-1">
                {successCount} uploaded successfully
              </p>
            )}
            <p className="text-xs text-gray-500 mt-2">Please wait...</p>
          </div>
        </div>
      )}
    </div>
  );
}
