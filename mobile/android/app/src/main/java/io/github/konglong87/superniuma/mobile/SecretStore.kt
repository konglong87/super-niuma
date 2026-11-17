package io.github.konglong87.superniuma.mobile

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** The model endpoint the person configured. [key] never leaves this process unencrypted except to the Go core. */
data class ModelRoute(val baseUrl: String, val model: String, val key: String)

/**
 * Keeps the model route. The key is encrypted with an AES-GCM key held by
 * the Android Keystore (not extractable); only the ciphertext is written to
 * app-private preferences, and backups are off for the app.
 */
class SecretStore(context: Context) {
    private val prefs = context.getSharedPreferences("model_route", Context.MODE_PRIVATE)

    fun save(route: ModelRoute) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val sealed = cipher.iv + cipher.doFinal(route.key.toByteArray(Charsets.UTF_8))
        prefs.edit()
            .putString("base_url", route.baseUrl)
            .putString("model", route.model)
            .putString("key", Base64.encodeToString(sealed, Base64.NO_WRAP))
            .apply()
    }

    /** The saved route, or null when none is saved or it cannot be decrypted. */
    fun load(): ModelRoute? {
        val baseUrl = prefs.getString("base_url", null) ?: return null
        val model = prefs.getString("model", null) ?: return null
        val sealed = prefs.getString("key", null)?.let { Base64.decode(it, Base64.NO_WRAP) } ?: return null
        if (sealed.size <= IV_BYTES) return null
        return try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed, 0, IV_BYTES))
            val plain = cipher.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES)
            ModelRoute(baseUrl, model, String(plain, Charsets.UTF_8))
        } catch (e: Exception) {
            null
        }
    }

    fun hasRoute(): Boolean = prefs.contains("key")

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getEntry(ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val ALIAS = "niuma_model_route"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
    }
}
