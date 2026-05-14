import org.jetbrains.intellij.platform.gradle.TestFrameworkType

plugins {
    id("org.jetbrains.kotlin.jvm") version "1.9.22"
    id("org.jetbrains.intellij.platform") version "2.2.0"
}

group   = "com.dullkingsman"
version = "0.1.0"

repositories {
    mavenCentral()
    intellijPlatform { defaultRepositories() }
}

dependencies {
    intellijPlatform {
        // Minimum supported IDE: IntelliJ IDEA 2023.1 (build 231)
        intellijIdeaCommunity("2023.1")
        instrumentationTools()
        testFramework(TestFrameworkType.Platform)
    }
}

intellijPlatform {
    pluginConfiguration {
        id          = "com.dullkingsman.dpg"
        name        = "DPG — Declarative PG"
        version     = project.version.toString()
        description = "Language support for .dpg (DPG / Declarative PG) source files."

        ideaVersion {
            sinceBuild = "231"
            untilBuild = provider { null }
        }

        vendor {
            name = "dullkingsman"
            url  = "https://github.com/dullkingsman/dpg"
        }
    }

    publishing {
        token = providers.environmentVariable("JETBRAINS_TOKEN")
    }

    signing {
        certificateChain = providers.environmentVariable("CERTIFICATE_CHAIN")
        privateKey        = providers.environmentVariable("PRIVATE_KEY")
        password          = providers.environmentVariable("PRIVATE_KEY_PASSWORD")
    }
}

kotlin {
    jvmToolchain(17)
}
