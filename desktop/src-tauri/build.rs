fn main() {
    tauri_build::build();
    #[cfg(target_os = "linux")]
    {
        // WebKitGTK may reference GBM without exposing it through pkg-config.
        // Use the multiarch library directly because embedded Linux images can
        // install a vendor /usr/lib/libgbm.so that shadows Mesa's implementation.
        let target = std::env::var("TARGET").unwrap_or_default();
        let multiarch = if target.starts_with("aarch64") { "aarch64-linux-gnu" }
            else if target.starts_with("x86_64") { "x86_64-linux-gnu" }
            else if target.starts_with("arm") { "arm-linux-gnueabihf" }
            else { "" };
        let path = format!("/usr/lib/{multiarch}/libgbm.so.1");
        if std::path::Path::new(&path).exists() { println!("cargo:rustc-link-arg={path}"); }
        else { println!("cargo:rustc-link-lib=gbm"); }
    }
}
