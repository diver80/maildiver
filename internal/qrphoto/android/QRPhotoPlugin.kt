package dev.mygo

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.provider.MediaStore
import androidx.core.content.FileProvider
import org.json.JSONObject
import java.io.File

// Takes a photo with the system's camera app, for reading a QR code. Unlike mygo's imagepicker
// it does not ask resolveActivity first: since Android 11 that answers null for every camera
// app unless the manifest lists the intent under <queries>, which plugins cannot add. Starting
// the intent and catching ActivityNotFoundException tells the truth.
class QRPhotoPlugin : MyGoPlugin {
    override fun call(method: String, args: JSONObject, reply: PluginReply) {
        when (method) {
            "capture" -> capture(reply)
            else -> reply.failure(PluginFailure("NoMethod", method))
        }
    }

    private fun capture(reply: PluginReply) {
        val ctx = PluginHost.context
        val dir = File(ctx.cacheDir, "camera").apply { mkdirs() }
        val f = File(dir, "${System.nanoTime()}.jpg")
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".mygo.files", f)
        val i = Intent(MediaStore.ACTION_IMAGE_CAPTURE)
            .putExtra(MediaStore.EXTRA_OUTPUT, uri)
            .addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_READ_URI_PERMISSION)
        try {
            PluginHost.startForResult(i) { code, _ ->
                reply.success(JSONObject().put("path", if (code == Activity.RESULT_OK && f.length() > 0) f.absolutePath else ""))
            }
        } catch (e: ActivityNotFoundException) {
            reply.failure(PluginFailure("NoCamera", "the device has no camera app"))
        } catch (e: SecurityException) {
            reply.failure(PluginFailure("NoCamera", e.toString()))
        }
    }
}
