# kotlinx.serialization keeps the generated serializers reachable through reflection on
# the companion; without this the release build decodes nothing.
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**
-keepclassmembers class app.pod.api.generated.** {
    *** Companion;
}
-keepclasseswithmembers class app.pod.api.generated.** {
    kotlinx.serialization.KSerializer serializer(...);
}
