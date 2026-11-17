import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.github.konglong87.superniuma.mobile"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.github.konglong87.superniuma.mobile"
        minSdk = 29
        targetSdk = 35
        versionCode = 1
        versionName = "0.0.1-m0"
        ndk {
            abiFilters += "arm64-v8a"
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    buildFeatures {
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    // The Go core, built by ../build-core.sh with gomobile.
    implementation(files("libs/niumacore.aar"))
    testImplementation("junit:junit:4.13.2")
    // The Android framework's org.json is a stub in JVM unit tests.
    testImplementation("org.json:json:20240303")
}
