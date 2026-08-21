plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.hilt)
    alias(libs.plugins.ksp)
}

// apiBaseUrl reads the backend address from -PpodApiBaseUrl or POD_API_BASE_URL, falling
// back to the build type's default.
//
// Validated here rather than at runtime: a typo that reaches the app is a sign-in screen
// that fails for a reason nobody can see from the outside, and this is a build that can
// simply refuse. Cleartext is allowed for the debug build only (see the debug manifest) —
// a release build putting somebody's session token on plain HTTP is not a configuration
// option.
fun apiBaseUrl(default: String): String {
    // Blank counts as unset, not as an override of nothing. A CI input nobody filled in
    // arrives as "", and the first run of the apk workflow failed on this very check —
    // refusing an empty string is right for a typo and wrong for "I did not set this".
    val configured = (project.findProperty("podApiBaseUrl") as String?)?.takeIf { it.isNotBlank() }
        ?: System.getenv("POD_API_BASE_URL")?.takeIf { it.isNotBlank() }
        ?: return default
    val trimmed = configured.trim().trimEnd('/')
    require(trimmed.startsWith("http://") || trimmed.startsWith("https://")) {
        "podApiBaseUrl must start with http:// or https://, got \"$configured\""
    }
    return trimmed
}

android {
    namespace = "app.pod"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.pod"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        debug {
            // 10.0.2.2 is the emulator's alias for the host machine and means nothing on a
            // real phone, so a debug APK installed on a device needs to be told where the
            // backend is: `./gradlew :app:assembleDebug -PpodApiBaseUrl=http://192.168.1.5:8080`,
            // or POD_API_BASE_URL in the environment.
            buildConfigField("String", "API_BASE_URL", "\"${apiBaseUrl("http://10.0.2.2:8080")}\"")
        }
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            buildConfigField("String", "API_BASE_URL", "\"${apiBaseUrl("https://api.pod.app")}\"")
        }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(project(":core:api"))
    implementation(libs.ktor.client.okhttp)
    implementation(libs.androidx.security.crypto)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.core)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)

    implementation(libs.hilt.android)
    implementation(libs.hilt.navigation.compose)
    ksp(libs.hilt.compiler)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
}
