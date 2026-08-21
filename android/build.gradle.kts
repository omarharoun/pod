// Deliberately empty.
//
// Plugin versions come from gradle/libs.versions.toml and each module applies what it
// needs. Keeping the Android plugins out of the root build means :core:api — which is a
// plain Kotlin/JVM module — can be configured and tested without the Android SDK present:
//
//     ./gradlew :core:api:test --configure-on-demand
