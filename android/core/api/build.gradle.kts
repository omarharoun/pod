plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
}

// Deliberately a plain Kotlin/JVM module, not an Android library.
//
// The contract, the copy catalogue, the tokens and the sign-in state machine have nothing
// to do with Android, and keeping them off the platform means their tests are ordinary
// JVM tests: no emulator, no Robolectric, seconds not minutes. Anything that genuinely
// needs a Context — the encrypted token store — lives in :app instead.

java {
    // Bytecode level 17, to match what :app consumes. Deliberately not a toolchain spec:
    // that would pin contributors and CI to one exact JDK for no benefit.
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    // Generated models and the client are this module's whole surface: make every
    // visibility deliberate rather than accidentally public. The DSL form applies to
    // main only, which is what we want — tests are not API.
    explicitApi()

    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    api(libs.kotlinx.serialization.json)
    api(libs.kotlinx.datetime)
    api(libs.kotlinx.coroutines.core)
    api(libs.ktor.client.core)
    implementation(libs.ktor.client.content.negotiation)
    implementation(libs.ktor.serialization.kotlinx.json)

    testImplementation(libs.junit)
    testImplementation(libs.ktor.client.mock)
    testImplementation(libs.kotlinx.coroutines.test)
}

tasks.withType<Test>().configureEach {
    useJUnit()
}
