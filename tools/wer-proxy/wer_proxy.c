#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif

#include <stddef.h>
#include <stdarg.h>
#include <stdio.h>
#include <limits.h>
#include <string.h>
#include <wchar.h>
#include <windows.h>
#include <werapi.h>

#define ARRAY_COUNT(value) (sizeof(value) / sizeof((value)[0]))
#ifndef DWORD_MAX
#define DWORD_MAX ((DWORD)~0UL)
#endif

typedef HRESULT(WINAPI *RealWerReportCreate)(PCWSTR, WER_REPORT_TYPE, PWER_REPORT_INFORMATION, HREPORT *);
typedef HRESULT(WINAPI *RealWerReportSetParameter)(HREPORT, DWORD, PCWSTR, PCWSTR);
typedef HRESULT(WINAPI *RealWerReportAddFile)(HREPORT, PCWSTR, WER_FILE_TYPE, DWORD);
typedef HRESULT(WINAPI *RealWerReportSubmit)(HREPORT, WER_CONSENT, DWORD, PWER_SUBMIT_RESULT);

typedef union ProcAddressCast {
    FARPROC proc;
    RealWerReportCreate report_create;
    RealWerReportSetParameter report_set_parameter;
    RealWerReportAddFile report_add_file;
    RealWerReportSubmit report_submit;
} ProcAddressCast;

static HMODULE g_module;
static HMODULE g_real_wer;
static HMODULE g_companion_module;
static RealWerReportCreate g_real_report_create;
static RealWerReportSetParameter g_real_report_set_parameter;
static RealWerReportAddFile g_real_report_add_file;
static RealWerReportSubmit g_real_report_submit;
static SRWLOCK g_log_lock = SRWLOCK_INIT;
static SRWLOCK g_load_lock = SRWLOCK_INIT;
static LONG g_call_id;

static const char *wide_to_utf8(PCWSTR value, char *buffer, size_t buffer_len);
static void log_format(const char *format, ...);

static size_t bounded_wcslen_local(const WCHAR *value, size_t max_len) {
    size_t index = 0;

    if (value == NULL) {
        return 0;
    }

    while (index < max_len && value[index] != L'\0') {
        index++;
    }

    return index;
}

static void copy_wstr(WCHAR *dst, size_t dst_cch, const WCHAR *src) {
    size_t index = 0;

    if (dst == NULL || dst_cch == 0) {
        return;
    }

    if (src == NULL) {
        dst[0] = L'\0';
        return;
    }

    while (index + 1 < dst_cch && src[index] != L'\0') {
        dst[index] = src[index];
        index++;
    }
    dst[index] = L'\0';
}

static BOOL append_wstr(WCHAR *dst, size_t dst_cch, const WCHAR *src) {
    size_t used = bounded_wcslen_local(dst, dst_cch);
    size_t index = 0;

    if (dst == NULL || src == NULL || used >= dst_cch) {
        return FALSE;
    }

    while (used + index + 1 < dst_cch && src[index] != L'\0') {
        dst[used + index] = src[index];
        index++;
    }

    if (src[index] != L'\0') {
        return FALSE;
    }

    dst[used + index] = L'\0';
    return TRUE;
}

static void build_sibling_path(WCHAR *out, size_t out_cch, const WCHAR *leaf_name) {
    WCHAR base[MAX_PATH];
    DWORD length = 0;
    size_t base_len = 0;

    if (out == NULL || out_cch == 0) {
        return;
    }

    out[0] = L'\0';
    base[0] = L'\0';

    if (g_module != NULL) {
        length = GetModuleFileNameW(g_module, base, (DWORD)ARRAY_COUNT(base));
    }

    if (length == 0 || length >= ARRAY_COUNT(base)) {
        length = GetTempPathW((DWORD)ARRAY_COUNT(base), base);
        if (length == 0 || length >= ARRAY_COUNT(base)) {
            copy_wstr(out, out_cch, leaf_name);
            return;
        }
    } else {
        base_len = bounded_wcslen_local(base, ARRAY_COUNT(base));
        while (base_len > 0 && base[base_len - 1] != L'\\' && base[base_len - 1] != L'/') {
            base_len--;
        }
        base[base_len] = L'\0';
    }

    copy_wstr(out, out_cch, base);
    if (!append_wstr(out, out_cch, leaf_name)) {
        copy_wstr(out, out_cch, leaf_name);
    }
}

static void log_process_paths(const char *prefix) {
    WCHAR path[MAX_PATH];
    char utf8[2048];

    if (prefix == NULL) {
        prefix = "path";
    }

    path[0] = L'\0';
    if (GetModuleFileNameW(NULL, path, (DWORD)ARRAY_COUNT(path)) > 0) {
        log_format("%s executable=%s", prefix, wide_to_utf8(path, utf8, sizeof(utf8)));
    }
    path[0] = L'\0';
    if (g_module != NULL && GetModuleFileNameW(g_module, path, (DWORD)ARRAY_COUNT(path)) > 0) {
        log_format("%s shim=%s", prefix, wide_to_utf8(path, utf8, sizeof(utf8)));
    }
    log_format("%s command_line=%s", prefix, wide_to_utf8(GetCommandLineW(), utf8, sizeof(utf8)));
}

static void load_companion_dreadnought_dll(void) {
    WCHAR path[MAX_PATH];
    char utf8[2048];
    DWORD attributes;

    build_sibling_path(path, ARRAY_COUNT(path), L"Dreadnought.dll");
    attributes = GetFileAttributesW(path);
    if (attributes == INVALID_FILE_ATTRIBUTES) {
        log_format("companion Dreadnought.dll not found path=%s error=%lu", wide_to_utf8(path, utf8, sizeof(utf8)), GetLastError());
        return;
    }
    if ((attributes & FILE_ATTRIBUTE_DIRECTORY) != 0) {
        log_format("companion Dreadnought.dll path is a directory path=%s", wide_to_utf8(path, utf8, sizeof(utf8)));
        return;
    }

    g_companion_module = LoadLibraryExW(path, NULL, LOAD_WITH_ALTERED_SEARCH_PATH);
    if (g_companion_module == NULL) {
        log_format("LoadLibraryExW(Dreadnought.dll) failed path=%s error=%lu", wide_to_utf8(path, utf8, sizeof(utf8)), GetLastError());
        return;
    }

    log_format("LoadLibraryExW(Dreadnought.dll) succeeded path=%s module=%p", wide_to_utf8(path, utf8, sizeof(utf8)), g_companion_module);
}

static DWORD WINAPI startup_thread(LPVOID param) {
    (void)param;

    log_format("wer shim loaded");
    log_process_paths("startup");
    load_companion_dreadnought_dll();
    return 0;
}

static BOOL build_system_wer_path(WCHAR *out, size_t out_cch) {
    UINT length;

    if (out == NULL || out_cch == 0 || out_cch > UINT_MAX) {
        return FALSE;
    }

    out[0] = L'\0';
    length = GetSystemDirectoryW(out, (UINT)out_cch);
    if (length == 0 || length >= out_cch) {
        out[0] = L'\0';
        return FALSE;
    }

    return append_wstr(out, out_cch, L"\\wer.dll");
}

static const char *wide_to_utf8(PCWSTR value, char *buffer, size_t buffer_len) {
    int written;

    if (value == NULL) {
        return "(null)";
    }

    if (buffer == NULL || buffer_len == 0 || buffer_len > INT_MAX) {
        return "";
    }

    buffer[0] = '\0';
    written = WideCharToMultiByte(CP_UTF8, 0, value, -1, buffer, (int)buffer_len, NULL, NULL);
    if (written == 0) {
        snprintf(buffer, buffer_len, "<utf8-conversion-error:%lu>", GetLastError());
    }

    return buffer;
}

static void write_all(HANDLE file, const char *text) {
    DWORD ignored = 0;
    size_t len;

    if (file == INVALID_HANDLE_VALUE || text == NULL) {
        return;
    }

    len = strlen(text);
    if (len > 0 && len <= DWORD_MAX) {
        (void)WriteFile(file, text, (DWORD)len, &ignored, NULL);
    }
}

static void write_format(HANDLE file, const char *format, ...) {
    char buffer[4096];
    va_list args;

    va_start(args, format);
    (void)vsnprintf(buffer, sizeof(buffer), format, args);
    va_end(args);
    buffer[sizeof(buffer) - 1] = '\0';

    write_all(file, buffer);
}

static void write_wide_field(HANDLE file, const char *name, PCWSTR value) {
    char utf8[2048];

    write_format(file, "%s: %s\r\n", name, wide_to_utf8(value, utf8, sizeof(utf8)));
}

static void log_line(const char *line) {
    WCHAR path[MAX_PATH];
    HANDLE file;
    DWORD ignored = 0;
    size_t len;

    if (line == NULL) {
        return;
    }

    build_sibling_path(path, ARRAY_COUNT(path), L"dreadnought_wer_proxy.log");

    AcquireSRWLockExclusive(&g_log_lock);
    file = CreateFileW(
        path,
        FILE_APPEND_DATA,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
        NULL,
        OPEN_ALWAYS,
        FILE_ATTRIBUTE_NORMAL,
        NULL);
    if (file != INVALID_HANDLE_VALUE) {
        len = strlen(line);
        if (len > 0 && len <= DWORD_MAX) {
            (void)WriteFile(file, line, (DWORD)len, &ignored, NULL);
        }
        CloseHandle(file);
    }
    ReleaseSRWLockExclusive(&g_log_lock);
}

static void log_format(const char *format, ...) {
    SYSTEMTIME now;
    char buffer[4096];
    size_t offset;
    size_t len;
    va_list args;
    int prefix_len;

    GetLocalTime(&now);
    prefix_len = snprintf(
        buffer,
        sizeof(buffer),
        "%04u-%02u-%02u %02u:%02u:%02u.%03u pid=%lu tid=%lu ",
        now.wYear,
        now.wMonth,
        now.wDay,
        now.wHour,
        now.wMinute,
        now.wSecond,
        now.wMilliseconds,
        GetCurrentProcessId(),
        GetCurrentThreadId());
    if (prefix_len < 0) {
        buffer[0] = '\0';
        offset = 0;
    } else if ((size_t)prefix_len >= sizeof(buffer)) {
        buffer[sizeof(buffer) - 1] = '\0';
        offset = strlen(buffer);
    } else {
        offset = (size_t)prefix_len;
    }

    va_start(args, format);
    if (offset < sizeof(buffer)) {
        (void)vsnprintf(buffer + offset, sizeof(buffer) - offset, format, args);
    }
    va_end(args);
    buffer[sizeof(buffer) - 1] = '\0';

    len = strlen(buffer);
    if (len + 2 < sizeof(buffer)) {
        buffer[len] = '\r';
        buffer[len + 1] = '\n';
        buffer[len + 2] = '\0';
    } else if (sizeof(buffer) >= 3) {
        buffer[sizeof(buffer) - 3] = '\r';
        buffer[sizeof(buffer) - 2] = '\n';
        buffer[sizeof(buffer) - 1] = '\0';
    }

    log_line(buffer);
}

static void log_report_information(const WER_REPORT_INFORMATION *info) {
    char consent_key[1024];
    char event_name[1024];
    char app_name[1024];
    char app_path[2048];
    char description[2048];

    if (info == NULL) {
        log_format("report_information=(null)");
        return;
    }

    log_format(
        "report_information size=%lu hProcess=%p hwndParent=%p consentKey=%s friendlyEventName=%s applicationName=%s applicationPath=%s description=%s",
        info->dwSize,
        info->hProcess,
        info->hwndParent,
        wide_to_utf8(info->wzConsentKey, consent_key, sizeof(consent_key)),
        wide_to_utf8(info->wzFriendlyEventName, event_name, sizeof(event_name)),
        wide_to_utf8(info->wzApplicationName, app_name, sizeof(app_name)),
        wide_to_utf8(info->wzApplicationPath, app_path, sizeof(app_path)),
        wide_to_utf8(info->wzDescription, description, sizeof(description)));
}

static HRESULT ensure_real_wer(void) {
    WCHAR path[MAX_PATH];
    ProcAddressCast cast;
    DWORD error = ERROR_SUCCESS;
    HMODULE loaded = NULL;

    if (!build_system_wer_path(path, ARRAY_COUNT(path))) {
        return HRESULT_FROM_WIN32(ERROR_PATH_NOT_FOUND);
    }

    AcquireSRWLockExclusive(&g_load_lock);
    if (g_real_wer == NULL) {
        loaded = LoadLibraryW(path);
        if (loaded == NULL) {
            error = GetLastError();
        } else {
            cast.proc = GetProcAddress(loaded, "WerReportCreate");
            g_real_report_create = cast.report_create;
            cast.proc = GetProcAddress(loaded, "WerReportSetParameter");
            g_real_report_set_parameter = cast.report_set_parameter;
            cast.proc = GetProcAddress(loaded, "WerReportAddFile");
            g_real_report_add_file = cast.report_add_file;
            cast.proc = GetProcAddress(loaded, "WerReportSubmit");
            g_real_report_submit = cast.report_submit;
            g_real_wer = loaded;
        }
    }
    ReleaseSRWLockExclusive(&g_load_lock);

    if (error != ERROR_SUCCESS) {
        char utf8[2048];
        log_format("LoadLibraryW(real WER) failed path=%s error=%lu", wide_to_utf8(path, utf8, sizeof(utf8)), error);
        return HRESULT_FROM_WIN32(error);
    }

    return S_OK;
}

static BOOL write_diagnostics_file(WCHAR *out_path, size_t out_path_cch) {
    WCHAR path[MAX_PATH];
    WCHAR value[MAX_PATH];
    HANDLE file;
    SYSTEMTIME now;

    build_sibling_path(path, ARRAY_COUNT(path), L"dreadnought_wer_diagnostics.txt");
    if (out_path != NULL && out_path_cch > 0) {
        copy_wstr(out_path, out_path_cch, path);
    }

    file = CreateFileW(
        path,
        GENERIC_WRITE,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
        NULL,
        CREATE_ALWAYS,
        FILE_ATTRIBUTE_NORMAL,
        NULL);
    if (file == INVALID_HANDLE_VALUE) {
        char utf8[2048];
        log_format("diagnostics create failed path=%s error=%lu", wide_to_utf8(path, utf8, sizeof(utf8)), GetLastError());
        return FALSE;
    }

    GetLocalTime(&now);
    write_all(file, "Dreadnought WER proxy diagnostics\r\n");
    write_all(file, "This app-local wer.dll forwards calls to the real system WER DLL unchanged.\r\n");
    write_format(
        file,
        "timestamp_local: %04u-%02u-%02u %02u:%02u:%02u.%03u\r\n",
        now.wYear,
        now.wMonth,
        now.wDay,
        now.wHour,
        now.wMinute,
        now.wSecond,
        now.wMilliseconds);
    write_format(file, "process_id: %lu\r\n", GetCurrentProcessId());
    write_format(file, "thread_id: %lu\r\n", GetCurrentThreadId());
    write_format(file, "wer_proxy_call_count: %ld\r\n", g_call_id);

    if (GetModuleFileNameW(NULL, value, (DWORD)ARRAY_COUNT(value)) > 0) {
        write_wide_field(file, "executable", value);
    }
    if (g_module != NULL && GetModuleFileNameW(g_module, value, (DWORD)ARRAY_COUNT(value)) > 0) {
        write_wide_field(file, "proxy_dll", value);
    }
    if (g_real_wer != NULL && GetModuleFileNameW(g_real_wer, value, (DWORD)ARRAY_COUNT(value)) > 0) {
        write_wide_field(file, "real_wer_dll", value);
    } else if (build_system_wer_path(value, ARRAY_COUNT(value))) {
        write_wide_field(file, "real_wer_dll", value);
    }
    if (GetCurrentDirectoryW((DWORD)ARRAY_COUNT(value), value) > 0) {
        write_wide_field(file, "current_directory", value);
    }
    build_sibling_path(value, ARRAY_COUNT(value), L"dreadnought_wer_proxy.log");
    write_wide_field(file, "wer_proxy_log", value);
    write_wide_field(file, "command_line", GetCommandLineW());

    CloseHandle(file);
    return TRUE;
}

HRESULT WINAPI WerReportCreate(
    PCWSTR pwzEventType,
    WER_REPORT_TYPE repType,
    PWER_REPORT_INFORMATION pReportInformation,
    HREPORT *phReportHandle) {
    char event_type[1024];
    HRESULT hr;
    LONG call_id = InterlockedIncrement(&g_call_id);

    log_format(
        "#%ld WerReportCreate eventType=%s reportType=%d reportInfo=%p outHandle=%p",
        call_id,
        wide_to_utf8(pwzEventType, event_type, sizeof(event_type)),
        (int)repType,
        pReportInformation,
        phReportHandle);
    log_report_information(pReportInformation);

    hr = ensure_real_wer();
    if (FAILED(hr)) {
        if (phReportHandle != NULL) {
            *phReportHandle = NULL;
        }
        return hr;
    }
    if (g_real_report_create == NULL) {
        return HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND);
    }

    hr = g_real_report_create(pwzEventType, repType, pReportInformation, phReportHandle);
    log_format(
        "#%ld WerReportCreate returned hr=0x%08lx handle=%p",
        call_id,
        (unsigned long)hr,
        phReportHandle != NULL ? *phReportHandle : NULL);
    return hr;
}

HRESULT WINAPI WerReportSetParameter(HREPORT hReportHandle, DWORD dwparamID, PCWSTR pwzName, PCWSTR pwzValue) {
    char name[1024];
    char value[2048];
    HRESULT hr;
    LONG call_id = InterlockedIncrement(&g_call_id);

    log_format(
        "#%ld WerReportSetParameter handle=%p paramID=%lu name=%s value=%s",
        call_id,
        hReportHandle,
        dwparamID,
        wide_to_utf8(pwzName, name, sizeof(name)),
        wide_to_utf8(pwzValue, value, sizeof(value)));

    hr = ensure_real_wer();
    if (FAILED(hr)) {
        return hr;
    }
    if (g_real_report_set_parameter == NULL) {
        return HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND);
    }

    hr = g_real_report_set_parameter(hReportHandle, dwparamID, pwzName, pwzValue);
    log_format("#%ld WerReportSetParameter returned hr=0x%08lx", call_id, (unsigned long)hr);
    return hr;
}

HRESULT WINAPI WerReportAddFile(HREPORT hReportHandle, PCWSTR pwzPath, WER_FILE_TYPE repFileType, DWORD dwFileFlags) {
    char path[2048];
    HRESULT hr;
    LONG call_id = InterlockedIncrement(&g_call_id);

    log_format(
        "#%ld WerReportAddFile handle=%p path=%s fileType=%d flags=0x%08lx",
        call_id,
        hReportHandle,
        wide_to_utf8(pwzPath, path, sizeof(path)),
        (int)repFileType,
        dwFileFlags);

    hr = ensure_real_wer();
    if (FAILED(hr)) {
        return hr;
    }
    if (g_real_report_add_file == NULL) {
        return HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND);
    }

    hr = g_real_report_add_file(hReportHandle, pwzPath, repFileType, dwFileFlags);
    log_format("#%ld WerReportAddFile returned hr=0x%08lx", call_id, (unsigned long)hr);
    return hr;
}

HRESULT WINAPI WerReportSubmit(HREPORT hReportHandle, WER_CONSENT consent, DWORD dwFlags, PWER_SUBMIT_RESULT pSubmitResult) {
    WCHAR diagnostics_path[MAX_PATH];
    HRESULT hr;
    LONG call_id = InterlockedIncrement(&g_call_id);

    log_format(
        "#%ld WerReportSubmit handle=%p consent=%d flags=0x%08lx submitResult=%p",
        call_id,
        hReportHandle,
        (int)consent,
        dwFlags,
        pSubmitResult);

    hr = ensure_real_wer();
    if (FAILED(hr)) {
        return hr;
    }
    if (g_real_report_submit == NULL) {
        return HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND);
    }

    diagnostics_path[0] = L'\0';
    if (g_real_report_add_file != NULL && write_diagnostics_file(diagnostics_path, ARRAY_COUNT(diagnostics_path))) {
        char path[2048];
        HRESULT add_hr = g_real_report_add_file(hReportHandle, diagnostics_path, WerFileTypeOther, WER_FILE_ANONYMOUS_DATA);
        log_format(
            "#%ld attached diagnostics path=%s hr=0x%08lx",
            call_id,
            wide_to_utf8(diagnostics_path, path, sizeof(path)),
            (unsigned long)add_hr);
    }

    hr = g_real_report_submit(hReportHandle, consent, dwFlags, pSubmitResult);
    log_format(
        "#%ld WerReportSubmit returned hr=0x%08lx submitResult=%d",
        call_id,
        (unsigned long)hr,
        pSubmitResult != NULL ? (int)*pSubmitResult : 0);
    return hr;
}

BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID reserved) {
    HANDLE thread;

    (void)reserved;

    if (reason == DLL_PROCESS_ATTACH) {
        g_module = (HMODULE)instance;
        DisableThreadLibraryCalls(instance);
        thread = CreateThread(NULL, 0, startup_thread, NULL, 0, NULL);
        if (thread != NULL) {
            CloseHandle(thread);
        }
    }

    return TRUE;
}
