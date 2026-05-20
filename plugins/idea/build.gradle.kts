plugins {
    id("org.jetbrains.kotlin.jvm") version "1.9.22"
    id("org.jetbrains.intellij") version "1.17.2"
}

group   = "com.dullkingsman"
version = "0.2.0"

repositories {
    mavenCentral()
}

// If IDEA_LOCAL_PATH points to a locally-extracted IntelliJ IDEA Community
// 2023.1 distribution, use it directly (no network required).  Otherwise fall
// back to the standard version + type download, which works in CI and on any
// machine with JetBrains CDN access.
val ideaLocalPath = System.getenv("IDEA_LOCAL_PATH")

intellij {
    if (ideaLocalPath != null && file(ideaLocalPath).isDirectory) {
        localPath.set(ideaLocalPath)
    } else {
        version.set("2023.1")
        type.set("IC")
    }
    plugins.set(listOf<String>())
    instrumentCode.set(false)
}

tasks.patchPluginXml {
    sinceBuild.set("231")
    untilBuild.set("")
}

tasks.signPlugin {
    enabled = false
}

tasks.publishPlugin {
    token.set(System.getenv("JETBRAINS_TOKEN") ?: "")
}

// Target JVM 17 bytecode (required by IntelliJ Platform 2023.1) while compiling
// with whatever JDK is installed (21 in the dev environment).
kotlin {
    jvmToolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
    }
}
tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}
tasks.withType<JavaCompile>().configureEach {
    sourceCompatibility = "17"
    targetCompatibility = "17"
}
